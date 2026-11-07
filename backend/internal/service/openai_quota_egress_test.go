//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"github.com/stretchr/testify/require"
)

func TestEgressAuditOpenAIQuotaAttempts(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		for _, status := range []int{200, 429, 502} {
			t.Run(fmt.Sprintf("shadow=%t/status=%d", shadow, status), func(t *testing.T) {
				sink := captureAuditLogs(t)
				svc, _, upstream, id := quotaRefreshFixture(t, shadow, true)
				svc.openAIQuotaService.egressAuditor = NewEgressAuditor("", nil)
				upstream.status = 503
				upstream.modelStatus = status
				upstream.modelHeaders = fallbackQuotaHeaders()
				upstream.recoverAfterModel = shadow && status != 502
				_, err := svc.GetUsage(context.Background(), id)
				require.NoError(t, err)
				want := 2
				if shadow && status != 502 {
					want++
				}
				counts := map[string]int{}
				for _, row := range sink.snapshot() {
					event, _ := row["event"].(string)
					if event != "upstream_egress_started" && event != "upstream_egress_response" && event != "upstream_egress_completed" {
						continue
					}
					counts[event]++
					require.Equal(t, int64(100), row["account_id"])
					require.Equal(t, id, row["quota_account_id"])
					require.NotEmpty(t, row["attempt_id"])
					if row["purpose"] == "model_fallback" {
						require.NotEmpty(t, row["model"])
					}
					encoded, err := json.Marshal(row)
					require.NoError(t, err)
					require.NotContains(t, string(encoded), "sensitive-quota-token")
					require.NotContains(t, string(encoded), "parent-org")
				}
				for _, event := range []string{"upstream_egress_started", "upstream_egress_response", "upstream_egress_completed"} {
					require.Equal(t, want, counts[event], event)
				}
			})
		}
	}
}

func TestEgressAuditOpenAIQuotaReadOnlyAndMonitorNoProbe(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			sink := captureAuditLogs(t)
			svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
			proxyID := int64(71)
			repo.accounts[id].ProxyID = &proxyID
			svc.openAIQuotaService.proxyRepo = &monitorFixtureProxyRepo{}
			prober := &auditProber{}
			svc.openAIQuotaService.egressAuditor = NewEgressAuditor("", prober)
			upstream.status = status
			ctx := monitorrequest.With(context.Background(), monitorrequest.Policy{MonitorID: 9, ActorID: 10, Trigger: "manual", Route: "http://proxy.invalid:8080", Validate: func(context.Context) error { return nil }})
			_, err := svc.GetUsageForAccount(ctx, repo.accounts[id])
			if status == 503 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []string{"GET /backend-api/wham/usage"}, upstream.requestLog())
			require.Zero(t, prober.calls.Load())
			counts := map[string]int{}
			for _, row := range sink.snapshot() {
				if event, _ := row["event"].(string); event == "upstream_egress_started" || event == "upstream_egress_completed" {
					counts[event]++
					require.Equal(t, int64(9), row["monitor_id"])
					require.Equal(t, int64(10), row["actor_id"])
					require.Equal(t, int64(71), row["proxy_id"])
				}
			}
			require.Equal(t, 1, counts["upstream_egress_started"])
			require.Equal(t, 1, counts["upstream_egress_completed"])
		})
	}
}

func TestEgressAuditOpenAIQuotaTimeout(t *testing.T) {
	sink := captureAuditLogs(t)
	svc, _, upstream, id := quotaRefreshFixture(t, false, true)
	svc.openAIQuotaService.egressAuditor = NewEgressAuditor("", nil)
	upstream.timeout = true
	_, err := svc.GetUsage(context.Background(), id)
	require.NoError(t, err)
	completed, timedOut := 0, 0
	for _, row := range sink.snapshot() {
		if row["event"] == "upstream_egress_completed" {
			completed++
			if row["purpose"] == "quota_usage" {
				timedOut++
				require.Contains(t, []string{"timeout", "canceled", "transport_error"}, row["outcome"])
			} else {
				// The quota read timed out; the subsequent model request can
				// still receive headers and close its response normally.
				require.Equal(t, "body_closed", row["outcome"])
			}
		}
	}
	require.Equal(t, 1, timedOut)
	require.Equal(t, 2, completed)
}

func TestEgressAuditOpenAIQuotaCancellation(t *testing.T) {
	sink := captureAuditLogs(t)
	svc, _, upstream, id := quotaRefreshFixture(t, false, true)
	svc.openAIQuotaService.egressAuditor = NewEgressAuditor("", nil)
	upstream.requestStarted = make(chan struct{}, 1)
	upstream.releaseRequest = make(chan struct{})
	upstream.timeout = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := svc.openAIQuotaService.QueryUsage(withOpenAIQuotaUsageOnly(ctx), id); done <- err }()
	select {
	case <-upstream.requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	require.Error(t, <-done)
	completed := 0
	for _, row := range sink.snapshot() {
		if row["event"] == "upstream_egress_completed" {
			completed++
			require.Equal(t, "canceled", row["outcome"])
		}
	}
	require.Equal(t, 1, completed)
}
