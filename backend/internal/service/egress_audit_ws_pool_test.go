package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEgressAuditPoolHandshakeFailure(t *testing.T) {
	sink := captureAuditLogs(t)
	pool := newOpenAIWSConnPool(nil)
	t.Cleanup(pool.Close)
	pool.auditor = NewEgressAuditor("", nil)
	pool.clientDialer = &auditDialStub{status: 429, err: errors.New("rate limited")}
	lease, err := pool.Acquire(t.Context(), openAIWSAcquireRequest{
		Account: &Account{ID: 7, Name: "test account", Concurrency: 1},
		WSURL:   "wss://example.com/v1/responses",
	})
	require.Nil(t, lease)
	var dialErr *openAIWSDialError
	require.ErrorAs(t, err, &dialErr)
	require.Equal(t, 429, dialErr.StatusCode)
	var auditRows []map[string]any
	for _, row := range sink.snapshot() {
		if row["phase"] == "dial" {
			auditRows = append(auditRows, row)
		}
	}
	require.Len(t, auditRows, 3)
	require.Equal(t, "upstream_egress_started", auditRows[0]["event"])
	require.Equal(t, int64(429), auditRows[1]["status_code"])
	require.Equal(t, "transport_error", auditRows[2]["outcome"])
	require.Equal(t, auditRows[0]["attempt_id"], auditRows[2]["attempt_id"])
}
