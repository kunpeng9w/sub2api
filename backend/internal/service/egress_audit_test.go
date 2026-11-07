package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type auditLogSink struct {
	mu      sync.Mutex
	records []map[string]any
}

func (s *auditLogSink) Enabled(context.Context, slog.Level) bool { return true }
func (s *auditLogSink) Handle(_ context.Context, r slog.Record) error {
	v := map[string]any{"event": r.Message}
	r.Attrs(func(a slog.Attr) bool { v[a.Key] = a.Value.Any(); return true })
	s.mu.Lock()
	s.records = append(s.records, v)
	s.mu.Unlock()
	return nil
}
func (s *auditLogSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *auditLogSink) WithGroup(string) slog.Handler      { return s }
func (s *auditLogSink) snapshot() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any{}, s.records...)
}
func captureAuditLogs(t *testing.T) *auditLogSink {
	t.Helper()
	old := slog.Default()
	sink := &auditLogSink{}
	slog.SetDefault(slog.New(sink))
	t.Cleanup(func() { slog.SetDefault(old) })
	return sink
}

type auditProber struct {
	calls   atomic.Int64
	release chan struct{}
	fail    bool
}

func (p *auditProber) ProbeProxy(ctx context.Context, _ string) (*ProxyExitInfo, int64, error) {
	p.calls.Add(1)
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if p.fail {
		return nil, 0, errors.New("secret error")
	}
	return &ProxyExitInfo{IP: "203.0.113.20"}, 1, nil
}

func TestEgressAuditNamesRedactionAndStreaming(t *testing.T) {
	sink := captureAuditLogs(t)
	a := NewEgressAuditor("http://127.0.0.1:7890", nil)
	id := int64(7)
	account := &Account{ID: 42, Name: "模型账号", ProxyID: &id, Proxy: &Proxy{ID: id, Name: "美国住宅出口"}}
	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-1")
	ctx = context.WithValue(ctx, ctxkey.UserID, int64(3))
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, int64(9))
	p := a.Begin(ctx, account, "socks5://secret-user:secret-pass@proxy.example:8022", "https://api.example/v1?token=hidden", "http")
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("stream"))}
	p.FinishHTTP(resp, nil)
	require.Len(t, sink.snapshot(), 2, "headers must not finish a stream")
	_, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Len(t, sink.snapshot(), 3, "EOF and Close finish only once")
	row := sink.snapshot()[0]
	require.Equal(t, "美国住宅出口", row["proxy_endpoint_name"])
	require.Equal(t, "socks5h://proxy.example:8022", row["proxy_endpoint"])
	require.Equal(t, int64(42), row["account_id"])
	require.Equal(t, int64(3), row["user_id"])
	require.Equal(t, "req-1", row["request_id"])
	for _, r := range sink.snapshot() {
		for _, v := range r {
			if text, ok := v.(string); ok {
				require.NotContains(t, text, "secret-")
				require.NotContains(t, text, "hidden")
			}
		}
	}
	a.Begin(ctx, nil, "http://127.0.0.1:7890", "https://api.anthropic.com", "http")
	row = sink.snapshot()[3]
	require.Equal(t, "Mihomo", row["proxy_name"])
	require.Equal(t, "unavailable", row["exit_ip_status"])
	require.Equal(t, "", row["exit_ip"])
	a.Begin(ctx, nil, "", "https://example.com", "http")
	require.Equal(t, "直连", sink.snapshot()[4]["proxy_name"])
}

func TestEgressAuditProbeNonblockingIsolationAndExpiry(t *testing.T) {
	captureAuditLogs(t)
	prober := &auditProber{release: make(chan struct{})}
	a := NewEgressAuditor("", prober)
	require.Equal(t, "pending", a.observe("socks5h://session-a:p@host:80").Status)
	require.Equal(t, "pending", a.observe("socks5h://session-b:p@host:80").Status)
	require.Eventually(t, func() bool { return prober.calls.Load() == 2 }, time.Second, time.Millisecond)
	for i := 0; i < 20; i++ {
		a.observe("socks5h://session-a:p@host:80")
	}
	require.Equal(t, int64(2), prober.calls.Load())
	require.Equal(t, "unknown", a.observe("third-route").Status, "concurrency is bounded")
	close(prober.release)
	require.Eventually(t, func() bool { return a.observe("socks5h://session-a:p@host:80").Status == "fresh" }, time.Second, time.Millisecond)
	obs := a.observe("socks5h://session-a:p@host:80")
	require.Equal(t, "203.0.113.20", obs.IP)
	require.False(t, obs.CheckedAt.IsZero())
	require.Equal(t, int64(2), prober.calls.Load())
	a.mu.Lock()
	for k, v := range a.cache {
		v.CheckedAt = time.Now().Add(-6 * time.Minute)
		a.cache[k] = v
	}
	a.mu.Unlock()
	require.Equal(t, "stale", a.observe("socks5h://session-a:p@host:80").Status)
	require.Eventually(t, func() bool { return prober.calls.Load() == 3 && len(a.slots) == 0 }, time.Second, time.Millisecond)
}

func TestEgressAuditFailureAndRetryHaveSeparateAttempts(t *testing.T) {
	sink := captureAuditLogs(t)
	a := NewEgressAuditor("", nil)
	for _, id := range []int64{1, 2} {
		p := a.Begin(context.Background(), &Account{ID: id}, "", "https://api.example", "http")
		p.Finish(context.DeadlineExceeded)
		p.Finish(nil)
	}
	rows := sink.snapshot()
	require.Len(t, rows, 4)
	require.NotEqual(t, rows[0]["attempt_id"], rows[2]["attempt_id"])
	require.Equal(t, "timeout", rows[1]["outcome"])
	prober := &auditProber{fail: true}
	a = NewEgressAuditor("", prober)
	a.observe("")
	require.Eventually(t, func() bool { return a.observe("").Status == "failed" }, time.Second, time.Millisecond)
	require.Equal(t, "", a.observe("").IP)
	require.Equal(t, int64(1), prober.calls.Load())
	require.Eventually(t, func() bool { return len(a.slots) == 0 }, time.Second, time.Millisecond)
}

type auditFrameStub struct {
	payload []byte
	err     error
}

func (s *auditFrameStub) WriteFrame(context.Context, coderws.MessageType, []byte) error { return s.err }
func (s *auditFrameStub) ReadFrame(context.Context) (coderws.MessageType, []byte, error) {
	return coderws.MessageText, s.payload, s.err
}
func (s *auditFrameStub) Close() error { return nil }
func TestEgressAuditWebSocketEveryTurnOnSameConnection(t *testing.T) {
	sink := captureAuditLogs(t)
	stub := &auditFrameStub{payload: []byte(`{"type":"response.completed"}`)}
	c := newEgressAuditFrameConn(stub, NewEgressAuditor("", nil), &Account{ID: 4}, "", "wss://example.com")
	for i := 0; i < 2; i++ {
		require.NoError(t, c.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
		_, _, err := c.ReadFrame(context.Background())
		require.NoError(t, err)
	}
	require.NoError(t, c.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"session.update"}`)))
	require.NoError(t, c.Close())
	rows := sink.snapshot()
	require.Len(t, rows, 4)
	require.Equal(t, rows[0]["conn_id"], rows[2]["conn_id"])
	require.NotEqual(t, rows[0]["attempt_id"], rows[2]["attempt_id"])
	require.Equal(t, int64(2), rows[2]["turn_id"])
	require.Equal(t, true, rows[2]["connection_reused"])
}

func TestEgressAuditRejectedRouteNeverProbes(t *testing.T) {
	sink := captureAuditLogs(t)
	prober := &auditProber{}
	a := NewEgressAuditor("", prober)
	a.Rejected(context.Background(), &Account{ID: 8}, "http://user:password@proxy.example:80", "https://api.example", "http")
	rows := sink.snapshot()
	require.Len(t, rows, 1)
	require.Equal(t, "upstream_egress_blocked", rows[0]["event"])
	require.Equal(t, "route_rejected", rows[0]["outcome"])
	require.Equal(t, int64(0), prober.calls.Load())
}

func TestEgressAuditWebSocketCancelThenNextTurn(t *testing.T) {
	for _, terminal := range []string{"response.cancelled", "response.canceled"} {
		t.Run(terminal, func(t *testing.T) {
			sink := captureAuditLogs(t)
			stub := &auditFrameStub{payload: []byte(`{"type":"` + terminal + `"}`)}
			c := newEgressAuditFrameConn(stub, NewEgressAuditor("", nil), &Account{ID: 4}, "", "wss://example.com")
			require.NoError(t, c.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
			_, _, err := c.ReadFrame(t.Context())
			require.NoError(t, err)
			rows := sink.snapshot()
			require.Len(t, rows, 2)
			require.Equal(t, "canceled", rows[1]["outcome"])
			require.Equal(t, rows[0]["attempt_id"], rows[1]["attempt_id"])
			stub.payload = []byte(`{"type":"response.completed"}`)
			require.NoError(t, c.WriteFrame(t.Context(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
			_, _, err = c.ReadFrame(t.Context())
			require.NoError(t, err)
			require.NoError(t, c.Close())
			rows = sink.snapshot()
			require.Len(t, rows, 4)
			require.Equal(t, "completed", rows[3]["outcome"])
			require.Equal(t, rows[2]["attempt_id"], rows[3]["attempt_id"])
			require.NotEqual(t, rows[0]["attempt_id"], rows[2]["attempt_id"])
		})
	}
}

type auditDialStub struct {
	conn   openAIWSClientConn
	status int
	err    error
	route  string
}

func (d *auditDialStub) Dial(_ context.Context, _ string, _ http.Header, route string) (openAIWSClientConn, int, http.Header, error) {
	d.route = route
	return d.conn, d.status, http.Header{"X-Test": {"preserved"}}, d.err
}

type auditDialConn struct{ openAIWSClientConn }

func TestEgressAuditWebSocketDialFailuresAndRetry(t *testing.T) {
	for _, protocol := range []string{"websocket", "websocket_passthrough"} {
		t.Run(protocol, func(t *testing.T) {
			sink := captureAuditLogs(t)
			auditor := NewEgressAuditor("", nil)
			route := "http://user:secret@proxy.example:8080"
			account := &Account{ID: 7}
			cases := []struct {
				status  int
				err     error
				conn    openAIWSClientConn
				outcome string
			}{
				{0, context.DeadlineExceeded, nil, "timeout"},
				{429, errors.New("handshake rejected"), nil, "transport_error"},
				{101, nil, nil, "transport_error"},
				{101, nil, &auditDialConn{}, "completed"},
			}
			ids := map[any]bool{}
			for _, tc := range cases {
				before := len(sink.snapshot())
				dialer := &auditDialStub{conn: tc.conn, status: tc.status, err: tc.err}
				conn, status, headers, err := dialWithEgressAudit(t.Context(), dialer, auditor, account, "wss://example.com", nil, route, protocol)
				require.Equal(t, tc.err, err)
				require.Equal(t, tc.conn, conn)
				require.Equal(t, tc.status, status)
				require.Equal(t, "preserved", headers.Get("X-Test"))
				require.Equal(t, route, dialer.route)
				rows := sink.snapshot()[before:]
				require.Equal(t, "upstream_egress_started", rows[0]["event"])
				require.Equal(t, "dial", rows[0]["phase"])
				require.Equal(t, "http://proxy.example:8080", rows[0]["proxy_endpoint"])
				require.Equal(t, int64(7), rows[0]["account_id"])
				require.False(t, ids[rows[0]["attempt_id"]])
				ids[rows[0]["attempt_id"]] = true
				last := rows[len(rows)-1]
				require.Equal(t, tc.outcome, last["outcome"])
				require.Equal(t, rows[0]["attempt_id"], last["attempt_id"])
				if tc.status > 0 {
					require.Len(t, rows, 3)
					require.Equal(t, int64(tc.status), rows[1]["status_code"])
				} else {
					require.Len(t, rows, 2)
				}
			}
		})
	}
}

func TestMonitorAuditNeverStartsExitIPProbe(t *testing.T) {
	prober := &auditProber{}
	auditor := NewEgressAuditor("", prober)
	ctx := monitorrequest.With(context.Background(), monitorrequest.Policy{Route: "http://proxy.invalid:8080"})
	attempt := auditor.Begin(ctx, &Account{ID: 1}, "http://proxy.invalid:8080", "https://upstream.invalid", "http")
	attempt.Finish(nil)
	if prober.calls.Load() != 0 {
		t.Fatal("monitor audit must never issue exit-IP queries")
	}
}
