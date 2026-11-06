package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func auditNetworkFields(p *EgressAuditAttempt) map[string]any {
	fields := p.networkFields()
	result := make(map[string]any)
	for i := 0; i < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok {
			panic("audit field key must be a string")
		}
		result[key] = fields[i+1]
	}
	return result
}

// Exercise the real HTTP transport against loopback: trace composition, TLS,
// body forwarding and response completion must survive the decorators.
func TestEgressAuditNetworkHTTPTransport(t *testing.T) {
	sink := captureAuditLogs(t)
	const payload = `{"secret":"never-log-this"}`
	received := make(chan string, 2)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		received <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		flusher.Flush()
		_, _ = io.WriteString(w, "data: last\n\n")
	}))
	defer srv.Close()
	client := srv.Client()
	defer client.CloseIdleConnections()
	for i := 0; i < 2; i++ {
		var existingTraceCalled atomic.Bool
		ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { existingTraceCalled.Store(true) }})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/?key=secret-query", strings.NewReader(payload))
		require.NoError(t, err)
		p := NewEgressAuditor("", nil).Begin(ctx, &Account{ID: 1}, "", req.URL.String(), "http")
		resp, err := client.Do(p.TraceRequest(req))
		p.FinishHTTP(resp, err)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, payload, <-received)
		require.Equal(t, "data: first\n\ndata: last\n\n", string(body))
		m := auditNetworkFields(p)
		require.Eventually(t, existingTraceCalled.Load, time.Second, time.Millisecond)
		require.Equal(t, int64(len(payload)), m["request_body_bytes"])
		require.Equal(t, "http_body_read", m["request_body_bytes_kind"])
		require.Equal(t, int64(len(body)), m["response_body_bytes"])
		require.Contains(t, m, "request_write_total_ms")
		require.Contains(t, m, "wait_after_request_written_ms")
		require.Contains(t, m, "response_headers_ms")
		if i == 0 {
			require.Equal(t, int64(1), m["tls_completed"])
		} else {
			require.NotContains(t, m, "tls_total_ms")
		}
		before := auditNetworkFields(p)
		p.observeResponseBytes(100)
		p.observeRequestBytes(100)
		require.Equal(t, before, auditNetworkFields(p), "completion freezes counters")
	}
	for _, row := range sink.snapshot() {
		encoded, err := json.Marshal(row)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "never-log-this")
		require.NotContains(t, string(encoded), "secret-query")
	}
}

func TestEgressAuditNetworkRequestReplay(t *testing.T) {
	captureAuditLogs(t)
	p := NewEgressAuditor("", nil).Begin(t.Context(), nil, "", "https://example.invalid", "http")
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid", strings.NewReader("replay-payload"))
	require.NoError(t, err)
	originalBody := req.Body
	traced := p.TraceRequest(req)
	require.Equal(t, originalBody, req.Body, "do not mutate the caller's request")
	for i := 0; i < 2; i++ {
		body := traced.Body
		if i > 0 {
			body, err = traced.GetBody()
			require.NoError(t, err)
		}
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Equal(t, "replay-payload", string(data))
		require.NoError(t, body.Close())
	}
	require.Equal(t, int64(2*len("replay-payload")), auditNetworkFields(p)["request_body_bytes"])
	require.Equal(t, int64(len("replay-payload")), auditNetworkFields(p)["request_content_length"])
}

func TestEgressAuditNetworkParallelConnectsAndFailures(t *testing.T) {
	captureAuditLogs(t)
	p := NewEgressAuditor("", nil).Begin(t.Context(), nil, "", "https://example.invalid", "http")
	trace := httptrace.ContextClientTrace(p.networkTraceContext(t.Context()))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			trace.ConnectStart("tcp", "same-endpoint")
			trace.ConnectDone("tcp", "same-endpoint", syscall.ECONNRESET)
			_ = auditNetworkFields(p)
		}()
	}
	wg.Wait()
	trace.TLSHandshakeStart()
	trace.TLSHandshakeDone(tls.ConnectionState{}, context.DeadlineExceeded)
	trace.ConnectStart("tcp", "pending-endpoint")
	p.Finish(context.DeadlineExceeded)
	m := auditNetworkFields(p)
	require.Equal(t, int64(9), m["connect_attempts"])
	require.Equal(t, int64(8), m["connect_completed"])
	require.Equal(t, int64(8), m["connect_failures"])
	require.Equal(t, 1, m["connect_pending"])
	require.Contains(t, m, "connect_pending_total_ms")
	require.Equal(t, "tls", m["last_network_error_phase"])
	require.Equal(t, "timeout", m["last_network_error_class"])
	trace.ConnectDone("tcp", "pending-endpoint", nil)
	require.Equal(t, m, auditNetworkFields(p))
}

func TestEgressAuditNetworkRecoveredFailureDoesNotMaskFinalPhase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		response  bool
		wantPhase string
	}{
		{name: "waiting after recovered write", wantPhase: "response_wait"},
		{name: "reading after recovered dial", response: true, wantPhase: "response_read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := captureAuditLogs(t)
			p := NewEgressAuditor("", nil).Begin(t.Context(), nil, "", "https://example.invalid", "http")
			trace := httptrace.ContextClientTrace(p.networkTraceContext(t.Context()))
			if tc.response {
				trace.ConnectStart("tcp", "losing-endpoint")
				trace.ConnectDone("tcp", "losing-endpoint", context.DeadlineExceeded)
			} else {
				trace.WroteRequest(httptrace.WroteRequestInfo{Err: context.DeadlineExceeded})
			}
			trace.WroteRequest(httptrace.WroteRequestInfo{})
			if tc.response {
				resp := &http.Response{StatusCode: 200, Body: &auditNetworkTimeoutBody{}}
				p.FinishHTTP(resp, nil)
				_, err := io.ReadAll(resp.Body)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NoError(t, resp.Body.Close())
			} else {
				p.Finish(context.DeadlineExceeded)
			}
			rows := sink.snapshot()
			completed := rows[len(rows)-1]
			require.Equal(t, "upstream_egress_completed", completed["event"])
			require.Equal(t, "timeout", completed["last_network_error_class"])
			require.Equal(t, tc.wantPhase, completed["last_network_error_phase"])
		})
	}
}

type auditNetworkTimeoutBody struct{}

func (*auditNetworkTimeoutBody) Read([]byte) (int, error) {
	return 0, context.DeadlineExceeded
}
func (*auditNetworkTimeoutBody) Close() error { return nil }

func TestEgressAuditNetworkMissingHooksAndBodyFailure(t *testing.T) {
	sink := captureAuditLogs(t)
	p := NewEgressAuditor("", nil).Begin(t.Context(), nil, "", "https://example.invalid", "http")
	require.NotContains(t, auditNetworkFields(p), "connect_total_ms")
	require.NotContains(t, auditNetworkFields(p), "request_body_bytes")
	resp := &http.Response{StatusCode: 200, Body: &auditNetworkFailBody{}}
	p.FinishHTTP(resp, nil)
	rows := sink.snapshot()
	require.Equal(t, "upstream_egress_response", rows[len(rows)-1]["event"])
	body, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Equal(t, "abc", string(body))
	require.NoError(t, resp.Body.Close())
	m := auditNetworkFields(p)
	require.Equal(t, int64(3), m["response_body_bytes"])
	require.Equal(t, "response_read", m["last_network_error_phase"])
	require.Equal(t, "unexpected_eof", m["last_network_error_class"])
}

type auditNetworkFailBody struct{}

func (*auditNetworkFailBody) Read(data []byte) (int, error) {
	return copy(data, "abc"), io.ErrUnexpectedEOF
}
func (*auditNetworkFailBody) Close() error { return nil }

func TestEgressAuditNetworkWebSocketFramesPerTurn(t *testing.T) {
	sink := captureAuditLogs(t)
	stub := &auditFrameStub{}
	c := newEgressAuditFrameConn(stub, NewEgressAuditor("", nil), &Account{ID: 1}, "", "wss://example.invalid")
	create := []byte(`{"type":"response.create","input":"never-log-this"}`)
	for i := 0; i < 2; i++ {
		require.NoError(t, c.WriteFrame(t.Context(), coderws.MessageText, create))
		for _, message := range []string{`{"type":"response.created"}`, `{"type":"response.completed"}`} {
			stub.payload = []byte(message)
			_, _, err := c.ReadFrame(t.Context())
			require.NoError(t, err)
		}
	}
	var completed []map[string]any
	for _, row := range sink.snapshot() {
		if row["event"] == "upstream_egress_completed" {
			completed = append(completed, row)
		}
	}
	require.Len(t, completed, 2)
	for _, row := range completed {
		require.Equal(t, int64(len(create)), row["request_body_bytes"])
		require.Equal(t, int64(len(`{"type":"response.created"}`)+len(`{"type":"response.completed"}`)), row["response_body_bytes"])
		require.Equal(t, int64(2), row["response_read_chunks"])
		require.Contains(t, row, "request_write_total_ms")
		require.Contains(t, row, "wait_after_request_written_ms")
	}
	require.NotEqual(t, completed[0]["attempt_id"], completed[1]["attempt_id"])
}

func TestEgressAuditNetworkJSONEncoderCountsWithoutChangingPayload(t *testing.T) {
	captureAuditLogs(t)
	p := NewEgressAuditor("", nil).Begin(t.Context(), nil, "", "wss://example.invalid", "websocket")
	value := map[string]any{"type": "response.create", "input": "<secret>&"}
	var original strings.Builder
	require.NoError(t, json.NewEncoder(&original).Encode(value))
	var sent []byte
	p.beginWSWrite()
	err := writeEgressAuditJSON(value, p, func(data []byte) error { sent = append([]byte(nil), data...); return nil })
	p.endWSWrite(err)
	require.NoError(t, err)
	require.Equal(t, original.String(), string(sent))
	require.Equal(t, int64(len(sent)), auditNetworkFields(p)["request_body_bytes"])
	// Failed delivery is attempted payload bytes, never fabricated wire delivery.
	err = writeEgressAuditJSON(value, p, func([]byte) error { return errors.New("secret-transport-error") })
	require.ErrorContains(t, err, "failed to write JSON message: failed to marshal JSON")
	require.Equal(t, int64(2*len(sent)), auditNetworkFields(p)["request_body_bytes"])
	p.Finish(err)
	encoded, marshalErr := json.Marshal(auditNetworkFields(p))
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "secret-transport-error")
}

func TestEgressAuditNetworkDurationsAreDistinct(t *testing.T) {
	p := &EgressAuditAttempt{started: time.Now().Add(-time.Second)}
	p.network.wroteRequest = p.started.Add(100 * time.Millisecond)
	p.network.firstByte = p.started.Add(600 * time.Millisecond)
	p.network.firstRead = p.started.Add(650 * time.Millisecond)
	p.network.lastRead = p.started.Add(900 * time.Millisecond)
	m := auditNetworkFields(p)
	require.Equal(t, float64(500), m["wait_after_request_written_ms"])
	require.Equal(t, float64(600), m["first_response_byte_ms"])
	require.Equal(t, float64(250), m["response_body_observed_ms"])
	require.NotContains(t, m, "request_write_total_ms", "unobserved is not zero")
	var absent *EgressAuditAttempt
	require.Equal(t, context.Background(), absent.networkTraceContext(context.Background()))
	absent.observeRequestBytes(1)
	absent.observeResponseBytes(1)
	absent.beginWSWrite()
	absent.endWSWrite(nil)
}
