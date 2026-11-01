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

func TestEgressAuditPoolConnectionReuseAcrossTurns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reused bool
		turns  []int
		want   []bool
	}{
		{"new connection", false, []int{1, 2, 3}, []bool{false, true, true}},
		{"borrowed existing connection", true, []int{1, 2}, []bool{true, true}},
		// A replacement connection can first be used at a later session turn.
		{"replacement connection", false, []int{9, 10}, []bool{false, true}},
		// HTTP-to-WS retries use attempt indices rather than session turn numbers.
		{"repeated attempt index", false, []int{0, 0, 1}, []bool{false, true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := captureAuditLogs(t)
			lease := &openAIWSConnLease{
				pool:         &openAIWSConnPool{auditor: NewEgressAuditor("", nil)},
				conn:         &openAIWSConn{id: "same-connection"},
				auditAccount: &Account{ID: 7},
				auditTarget:  "wss://example.com/v1/responses",
				reused:       tc.reused,
			}
			for _, turn := range tc.turns {
				lease.beginEgressAudit(t.Context(), turn).Finish(nil)
			}
			rows := sink.snapshot()
			require.Len(t, rows, len(tc.turns)*2)
			ids := make(map[any]bool)
			for i, turn := range tc.turns {
				started, completed := rows[i*2], rows[i*2+1]
				require.Equal(t, "upstream_egress_started", started["event"])
				require.Equal(t, "upstream_egress_completed", completed["event"])
				require.Equal(t, tc.want[i], started["connection_reused"])
				require.Equal(t, tc.want[i], completed["connection_reused"])
				require.Equal(t, "same-connection", started["conn_id"])
				require.Equal(t, int64(turn), started["turn_id"])
				require.Equal(t, started["attempt_id"], completed["attempt_id"])
				require.False(t, ids[started["attempt_id"]])
				ids[started["attempt_id"]] = true
			}
			require.Equal(t, tc.reused, lease.Reused(), "acquisition metrics retain their original meaning")
		})
	}
}
