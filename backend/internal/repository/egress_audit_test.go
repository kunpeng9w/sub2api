package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEgressAuditHTTPUsesCurrentProxyNameWithoutChangingBinding(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = io.WriteString(w, "data: ok\n\n") }))
	defer proxy.Close()
	p := strictEgressBoundProxy(t, proxy.URL)
	p.Name = "新的IP管理名称"
	stale := *p
	stale.Name = "旧名称"
	account := &service.Account{ID: 4, Name: "上游账号", ProxyID: &p.ID, Proxy: &stale}
	s, ok := strictEgressHTTPUpstream(t, nil, &strictEgressAccounts{value: account}, &strictEgressProxies{value: p}).(*httpUpstreamService)
	require.True(t, ok)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://upstream.invalid/v1", nil)
	require.NoError(t, err)
	route, snapshot, err := s.resolveEgressSnapshot(req, p.URL(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "新的IP管理名称", snapshot.Proxy.Name)
	require.Equal(t, "旧名称", account.Proxy.Name, "shared account was not mutated")
	for i := 0; i < 2; i++ {
		resp, err := s.Do(req.Clone(req.Context()), route, account.ID, 1)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.True(t, strings.Contains(string(body), "data: ok"))
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, int64(2), calls.Load(), "audit must not add synchronous or probe requests")
	requireNoUpstreamInFlight(t, s)
}

// This crosses the production binding check, upstream drain timer, body tracking
// and audit wrapper. Closing twice must release exactly once; a retry must check
// the current proxy again instead of using the old connection pool.
func TestEgressAuditDrainUpgradeRechecksBindingAndReleasesOnce(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_ = http.NewResponseController(w).Flush()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = io.WriteString(w, ": ping\n\n")
				_ = http.NewResponseController(w).Flush()
			}
		}
	}))
	defer proxy.Close()
	p := strictEgressBoundProxy(t, proxy.URL)
	account := &service.Account{ID: 4, ProxyID: &p.ID, Proxy: p}
	proxies := &strictEgressProxies{value: p}
	s := strictEgressHTTPUpstream(t, nil, &strictEgressAccounts{value: account}, proxies).(*httpUpstreamService)
	s.audit = service.NewEgressAuditor("", nil)
	client, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := pkghttputil.DetachUpstreamContext(client, 40*time.Millisecond)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.invalid/v1", nil)
	require.NoError(t, err)
	resp, err := s.Do(req, p.URL(), account.ID, 1)
	require.NoError(t, err)
	cancel()
	body, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, context.Canceled)
	require.Contains(t, string(body), ": ping")
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close())
	requireNoUpstreamInFlight(t, s)
	s.mu.RLock()
	for _, entry := range s.clients {
		require.Zero(t, atomic.LoadInt64(&entry.inFlight), "duplicate close must not decrement below zero")
	}
	s.mu.RUnlock()
	p.Status = "disabled"
	_, err = s.Do(req.Clone(context.Background()), p.URL(), account.ID, 1)
	require.ErrorIs(t, err, service.ErrAccountProxyUnavailable)
	require.EqualValues(t, 1, calls.Load())
}
