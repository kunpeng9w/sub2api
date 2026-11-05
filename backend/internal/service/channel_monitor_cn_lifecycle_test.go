//go:build unit

package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"github.com/stretchr/testify/require"
)

type monitorCancelUpstream struct {
	HTTPUpstream
	started  chan *http.Request
	canceled chan struct{}
	release  chan struct{}
}

func (u *monitorCancelUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.started <- req
	<-req.Context().Done()
	close(u.canceled)
	<-u.release // Model transport cleanup after cancellation.
	return nil, req.Context().Err()
}

func TestMonitorCNQueriesWaitForTransportExit(t *testing.T) {
	for _, mode := range []string{"quota", "balance"} {
		t.Run(mode, func(t *testing.T) {
			upstream := &monitorCancelUpstream{
				started: make(chan *http.Request, 1), canceled: make(chan struct{}), release: make(chan struct{}),
			}
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(upstream.release) }) })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			route := "http://proxy.invalid:8080"
			ctx = monitorrequest.With(ctx, monitorrequest.Policy{Route: route, Validate: func(context.Context) error { return nil }})
			account := codingAccount(PlatformKimi)
			account.Credentials["base_url"] = "https://api.kimi.com/coding/v1"
			if mode == "balance" {
				account = paygAccount(PlatformDeepseek)
			}
			account.ProxyID = int64Ptr(71)
			repo := &fakeCNProbeAccountRepo{account: account}
			done := make(chan error, 1)
			go func() {
				var err error
				if mode == "quota" {
					svc := NewCNProviderQuotaService(repo, &monitorFixtureProxyRepo{}, upstream, nil)
					_, err = svc.QueryUsageForAccount(ctx, account)
				} else {
					svc := NewCNProviderBalanceService(repo, &monitorFixtureProxyRepo{}, upstream, nil)
					_, err = svc.QueryBalanceForAccount(ctx, account)
				}
				done <- err
			}()
			select {
			case req := <-upstream.started:
				policy, ok := monitorrequest.From(req.Context())
				require.True(t, ok, "nested queries must retain the monitor policy")
				require.Equal(t, route, policy.Route)
				want, _ := ctx.Deadline()
				got, ok := req.Context().Deadline()
				require.True(t, ok)
				require.Equal(t, want, got, "nested queries must not extend the execution deadline")
			case err := <-done:
				t.Fatalf("query did not reach the fixture: %v", err)
			case <-time.After(time.Second):
				t.Fatal("query did not start")
			}
			cancel()
			select {
			case <-upstream.canceled:
			case <-time.After(time.Second):
				t.Fatal("transport context did not receive cancellation")
			}
			select {
			case err := <-done:
				t.Fatalf("query returned before transport cleanup: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			unblock.Do(func() { close(upstream.release) })
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("query did not finish after transport cleanup")
			}
		})
	}
}
