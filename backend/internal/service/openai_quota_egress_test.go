//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type quotaProbeUpstream struct {
	HTTPUpstream
	probe func(*http.Request, string, int64) (*http.Response, error)
}

func (u quotaProbeUpstream) Do(r *http.Request, route string, accountID int64, _ int) (*http.Response, error) {
	return u.probe(r, route, accountID)
}

func TestStrictEgressConcurrentAccountRefreshAndMonitorSampling(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		status        int
		used          float64
	}{
		{"sample", `{"rate_limit":{"primary_window":{"used_percent":12,"limit_window_seconds":18000,"reset_after_seconds":3600}}}`, 200, 12},
		{"missing percentage", `{"rate_limit":{"primary_window":{"limit_window_seconds":18000,"reset_after_seconds":3600}}}`, 200, 0},
		{"missing reset", `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000}}}`, 200, 0},
		{"request failed", `{}`, 503, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const route = "http://proxy.invalid:8080"
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			arrived := make(chan string, 2)
			sink := captureAuditLogs(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				arrived <- r.Method + " " + r.URL.Path
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.payload)
			}))
			defer upstream.Close()
			defer cancel()
			proxyID := int64(71)
			account := &Account{ID: 92, Type: AccountTypeOAuth, Platform: PlatformOpenAI, ProxyID: &proxyID,
				Proxy:       &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: StatusActive},
				Credentials: map[string]any{"access_token": "fixture-token", "chatgpt_account_id": "fixture-id"}}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}
			factory := newQuotaRedirectingFactory(upstream)
			quota := NewOpenAIQuotaService(repo, &monitorFixtureProxyRepo{}, NewOpenAITokenProvider(repo, nil, nil),
				func(proxy string) (*req.Client, error) {
					if proxy != route {
						return nil, fmt.Errorf("unexpected monitor route %q", proxy)
					}
					return factory(proxy)
				}, nil)
			quota.egressAuditor = NewEgressAuditor("", nil)
			svc := &AccountUsageService{accountRepo: repo, openAIQuotaService: quota, cache: NewUsageCache(),
				usageProbeUpstream: quotaProbeUpstream{probe: func(r *http.Request, proxy string, id int64) (*http.Response, error) {
					if r.Method != http.MethodPost || proxy != route || id != account.ID || monitorrequest.Active(r.Context()) {
						return nil, fmt.Errorf("unexpected account probe %s %q %d", r.Method, proxy, id)
					}
					arrived <- r.Method + " " + r.URL.Path
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader(""))}, nil
				}}}
			type result struct {
				usage *UsageInfo
				err   error
			}
			accountResult, monitorResult := make(chan result, 1), make(chan result, 1)
			go func() {
				usage, err := svc.GetUsage(ctx, account.ID, true)
				accountResult <- result{usage, err}
			}()
			go func() {
				pinned := monitorrequest.With(ctx, monitorrequest.Policy{MonitorID: 9, ActorID: 10, Trigger: "manual",
					Route: route, Validate: func(context.Context) error { return nil }})
				usage, err := svc.GetMonitorUsageForAccount(pinned, account)
				monitorResult <- result{usage, err}
			}()
			var requests []string
			for range 2 {
				select {
				case path := <-arrived:
					requests = append(requests, path)
				case <-ctx.Done():
					t.Fatalf("both requests must start independently: %v", requests)
				}
			}
			close(release)
			require.ElementsMatch(t, []string{"POST /backend-api/codex/responses", "GET /backend-api/wham/usage"}, requests)
			display := <-accountResult
			require.NoError(t, display.err)
			require.Equal(t, errorCodeQuotaRefreshFailed, display.usage.ErrorCode)
			monitor := <-monitorResult
			if tc.status != 200 || tc.name == "missing percentage" {
				require.Error(t, monitor.err)
			} else {
				require.NoError(t, monitor.err)
				require.NotNil(t, monitor.usage.FiveHour)
				require.Equal(t, tc.used, monitor.usage.FiveHour.Utilization)
				require.Nil(t, monitor.usage.SevenDay)
				if tc.name == "missing reset" {
					require.Nil(t, monitor.usage.FiveHour.ResetsAt)
				}
			}
			events := map[string]int{}
			for _, row := range sink.snapshot() {
				event, _ := row["event"].(string)
				if event == "upstream_egress_started" || event == "upstream_egress_completed" {
					events[event]++
					require.Equal(t, int64(9), row["monitor_id"])
					require.Equal(t, proxyID, row["proxy_id"])
				}
			}
			require.Equal(t, 1, events["upstream_egress_started"])
			require.Equal(t, 1, events["upstream_egress_completed"])
		})
	}
}
