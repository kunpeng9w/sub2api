package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// Exercise the real production JSON encoder and lease path, not just the audit
// helpers. The same physical connection serves two separately counted turns.
func TestEgressAuditNetworkProductionWebSocketLease(t *testing.T) {
	sink := captureAuditLogs(t)
	received := make(chan []byte, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for i := 0; i < 2; i++ {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			received <- data
			for _, event := range []string{`{"type":"response.created"}`, `{"type":"response.completed"}`} {
				if err := conn.Write(r.Context(), coderws.MessageText, []byte(event)); err != nil {
					return
				}
			}
		}
	}))
	defer srv.Close()
	auditor := NewEgressAuditor("", nil)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, _, err := dialWithEgressAudit(t.Context(), newDefaultOpenAIWSClientDialer(), auditor, &Account{ID: 1}, wsURL, nil, "", "websocket")
	require.NoError(t, err)
	defer client.Close()
	lease := &openAIWSConnLease{
		pool:         &openAIWSConnPool{auditor: auditor},
		conn:         newOpenAIWSConn("network-test", 1, client, nil),
		auditAccount: &Account{ID: 1}, auditTarget: wsURL,
		queueWait: 12 * time.Millisecond, connPick: 3 * time.Millisecond,
	}
	value := map[string]any{"type": "response.create", "input": "<never-log-this>"}
	var expected strings.Builder
	require.NoError(t, json.NewEncoder(&expected).Encode(value))
	for i := 1; i <= 2; i++ {
		attempt := lease.beginEgressAudit(t.Context(), i)
		require.NoError(t, lease.writeJSONWithEgressAudit(t.Context(), value, time.Second, attempt))
		for j := 0; j < 2; j++ {
			message, err := lease.ReadMessageWithContextTimeout(t.Context(), time.Second)
			require.NoError(t, err)
			attempt.observeResponseBytes(len(message))
		}
		attempt.Finish(nil)
		require.Equal(t, expected.String(), string(<-received))
		m := auditNetworkFields(attempt)
		require.Equal(t, int64(len(expected.String())), m["request_body_bytes"])
		require.Equal(t, int64(1), m["request_write_attempts"])
		require.Equal(t, int64(2), m["response_read_chunks"])
		require.Equal(t, "websocket_payload_attempted", m["request_body_bytes_kind"])
		require.Contains(t, m, "request_write_total_ms")
		require.Contains(t, m, "wait_after_request_written_ms")
	}
	for _, row := range sink.snapshot() {
		encoded, err := json.Marshal(row)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "never-log-this")
		if row["conn_id"] == "network-test" {
			require.Equal(t, float64(12), row["pool_queue_wait_ms"])
		}
	}
}
