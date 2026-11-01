package repository

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type strictEgressAccounts struct {
	service.AccountRepository
	value *service.Account
	err   error
}

func (r *strictEgressAccounts) GetByID(context.Context, int64) (*service.Account, error) {
	return r.value, r.err
}

type strictEgressProxies struct {
	service.ProxyRepository
	value *service.Proxy
	err   error
}

func (r *strictEgressProxies) GetByID(context.Context, int64) (*service.Proxy, error) {
	return r.value, r.err
}

func strictEgressBoundProxy(t *testing.T, raw string) *service.Proxy {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return &service.Proxy{ID: 71, Protocol: "http", Host: u.Hostname(), Port: port, Status: service.StatusActive}
}

func TestStrictEgressHTTPAndSSEFailureNeverUsesDirectOrBackup(t *testing.T) {
	var direct, backup, bound atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { direct.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { backup.Add(1); w.WriteHeader(200) }))
	defer other.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bound.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: simulated\n\n")
	}))
	p := strictEgressBoundProxy(t, proxy.URL)
	backupID := int64(72)
	p.FallbackMode = service.FallbackModeProxy
	p.BackupProxyID = &backupID
	a := &service.Account{ID: 4, Platform: service.PlatformOpenAI, ProxyID: &p.ID, Proxy: p}
	accounts, proxies := &strictEgressAccounts{value: a}, &strictEgressProxies{value: p}
	client := strictEgressHTTPUpstream(nil, accounts, proxies)
	for i := 0; i < 2; i++ {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
		require.NoError(t, err)
		resp, err := client.Do(req, proxy.URL, a.ID, 1)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Contains(t, string(body), "simulated")
	}
	proxy.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req, proxy.URL, a.ID, 1)
	require.Error(t, err)
	require.Equal(t, int64(2), bound.Load())
	require.Zero(t, direct.Load())
	require.Zero(t, backup.Load())
	require.Equal(t, int64(71), *a.ProxyID)
}

func TestStrictEgressTransportChecksLatestBindingBeforeAnyDial(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer proxy.Close()
	p := strictEgressBoundProxy(t, proxy.URL)
	a := &service.Account{ID: 4, ProxyID: &p.ID, Proxy: p}
	proxies := &strictEgressProxies{value: p}
	client := strictEgressHTTPUpstream(nil, &strictEgressAccounts{value: a}, proxies)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://upstream.invalid", nil)
	require.NoError(t, err)
	for _, scenario := range []string{"lookup", "missing", "disabled", "expired", "changed", "omitted"} {
		t.Run(scenario, func(t *testing.T) {
			copy := *p
			proxies.value = &copy
			proxies.err = nil
			selected := proxy.URL
			switch scenario {
			case "lookup":
				proxies.err = errors.New("simulated repository error")
			case "missing":
				proxies.value = nil
			case "disabled":
				copy.Status = "inactive"
			case "expired":
				past := time.Now().Add(-time.Minute)
				copy.ExpiresAt = &past
			case "changed":
				copy.Port++
			case "omitted":
				selected = ""
			}
			_, err := client.Do(req, selected, a.ID, 1)
			require.Error(t, err)
		})
	}
	require.Zero(t, calls.Load())
}

func TestClaudeEgressTransportSelectsManagedDefaultForUnboundAccounts(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	a := &service.Account{ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth}
	client, ok := strictEgressHTTPUpstream(cfg, &strictEgressAccounts{value: a}, &strictEgressProxies{}).(*httpUpstreamService)
	require.True(t, ok)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	require.NoError(t, err)
	route, err := client.resolveEgressForRequest(req, "", a.ID)
	require.NoError(t, err)
	require.Equal(t, cfg.Gateway.ClaudeEgress.ProxyURL, route)
	_, err = client.resolveEgressForRequest(req, "http://other.invalid:8080", a.ID)
	require.Error(t, err)
	a.Platform = service.PlatformOpenAI
	req.URL.Host = "normal.invalid"
	route, err = client.resolveEgressForRequest(req, "", a.ID)
	require.NoError(t, err)
	require.Empty(t, route)
}

func TestStrictEgressExpiryInvalidatesButNeverReassigns(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("UPDATE proxies SET status").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("(?s)UPDATE accounts.*SET extra").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4))
	mock.ExpectQuery("SELECT id FROM accounts WHERE proxy_id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(4))
	mock.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
	backup := int64(72)
	past := time.Now().Add(-time.Hour)
	p := service.Proxy{ID: 71, Status: service.StatusActive, FallbackMode: service.FallbackModeDirect, ExpiresAt: &past}
	r := &proxyRepository{}
	ids, err := r.sweepOneExpiredProxyOnExec(t.Context(), db, p, time.Now(), &backup, true)
	require.NoError(t, err)
	require.Equal(t, []int64{4}, ids)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStrictEgressDeleteSerializesAndRejectsBoundProxy(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	r := &proxyRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM proxies.*FOR UPDATE").WithArgs(int64(71)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(71))
	mock.ExpectQuery("SELECT COUNT.*FROM accounts").WithArgs(int64(71)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	require.ErrorIs(t, r.Delete(t.Context(), 71), service.ErrProxyInUse)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Simulate the scheduler selecting another account after one upstream failure.
// Both attempts share one transport service, but each retains its own route.
func TestStrictEgressCrossAccountRetryKeepsEachBindingAndInvalidatesChangedRoute(t *testing.T) {
	var direct, first, second atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { direct.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	failedProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { first.Add(1); w.WriteHeader(http.StatusBadGateway) }))
	defer failedProxy.Close()
	goodProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { second.Add(1); w.WriteHeader(http.StatusOK) }))
	defer goodProxy.Close()
	p1, p2 := strictEgressBoundProxy(t, failedProxy.URL), strictEgressBoundProxy(t, goodProxy.URL)
	p2.ID = 72
	a1 := &service.Account{ID: 4, ProxyID: &p1.ID, Proxy: p1}
	a2 := &service.Account{ID: 5, ProxyID: &p2.ID, Proxy: p2}
	accounts, proxies := &strictEgressAccounts{}, &strictEgressProxies{}
	client := strictEgressHTTPUpstream(nil, accounts, proxies)
	for _, attempt := range []struct {
		account *service.Account
		proxy   *service.Proxy
		status  int
	}{
		{a1, p1, http.StatusBadGateway}, {a2, p2, http.StatusOK}, {a2, p2, http.StatusOK},
	} {
		accounts.value, proxies.value = attempt.account, attempt.proxy
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
		require.NoError(t, err)
		response, err := client.Do(req, attempt.proxy.URL(), attempt.account.ID, 1)
		require.NoError(t, err)
		require.Equal(t, attempt.status, response.StatusCode)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}
	// An explicit admin endpoint change is valid for new requests. The old
	// route cannot keep using the cached connection, including internal retries.
	changed := *p2
	changed.Port = p1.Port
	proxies.value = &changed
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req, p2.URL(), a2.ID, 1)
	require.Error(t, err)
	response, err := client.Do(req, changed.URL(), a2.ID, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.Equal(t, int64(2), first.Load())
	require.Equal(t, int64(2), second.Load())
	require.Zero(t, direct.Load())
	require.Equal(t, int64(71), *a1.ProxyID)
	require.Equal(t, int64(72), *a2.ProxyID)
}

func TestStrictEgressProxyTimeoutNeverDialsDirect(t *testing.T) {
	var direct, bound atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { direct.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	release := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { bound.Add(1); <-release }))
	defer proxy.Close()
	defer close(release)
	p := strictEgressBoundProxy(t, proxy.URL)
	a := &service.Account{ID: 4, ProxyID: &p.ID, Proxy: p}
	client := strictEgressHTTPUpstream(nil, &strictEgressAccounts{value: a}, &strictEgressProxies{value: p})
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req, proxy.URL, a.ID, 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, int64(1), bound.Load())
	require.Zero(t, direct.Load())
	require.Equal(t, int64(71), *a.ProxyID)
}

func TestClaudeEgressRedirectCannotChangePinnedRoute(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	upstream, ok := NewHTTPUpstream(cfg).(*httpUpstreamService)
	require.True(t, ok)
	original, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://normal.invalid", nil)
	require.NoError(t, err)
	redirected, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
	require.NoError(t, err)
	// An unproxied request may not acquire a new route during a redirect.
	client := upstream.httpClientForUpstreamRequest(&http.Client{}, original, "")
	require.Error(t, client.CheckRedirect(redirected, []*http.Request{original}))
	for _, route := range []string{"http://account.invalid:8080", cfg.Gateway.ClaudeEgress.ProxyURL} {
		client := upstream.httpClientForUpstreamRequest(&http.Client{}, original, route)
		require.NoError(t, client.CheckRedirect(redirected, []*http.Request{original}))
		// Official-to-third-party redirects also retain the exact selected exit.
		client = upstream.httpClientForUpstreamRequest(&http.Client{}, redirected, route)
		require.NoError(t, client.CheckRedirect(original, []*http.Request{redirected}))
	}
}

func TestClaudeEgressBoundProxySurvivesManagedIngressLifecycle(t *testing.T) {
	var managedCalls, boundCalls, directCalls atomic.Int64
	newManaged := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			managedCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		}))
	}
	managed := newManaged()
	t.Cleanup(managed.Close)
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: managed.URL}}}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(origin.Close)
	bound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		boundCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: account-proxy\n\n")
	}))
	t.Cleanup(bound.Close)
	p := strictEgressBoundProxy(t, bound.URL)
	a := &service.Account{ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, ProxyID: &p.ID, Proxy: p}
	client := strictEgressHTTPUpstream(cfg, &strictEgressAccounts{value: a}, &strictEgressProxies{value: p})
	for _, state := range []string{"running", "stopped", "restarted"} {
		switch state {
		case "stopped":
			managed.Close()
		case "restarted":
			managed = newManaged()
			t.Cleanup(managed.Close)
			cfg.Gateway.ClaudeEgress.ProxyURL = managed.URL
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL, nil)
		require.NoError(t, err)
		selected, err := service.ResolveAccountProxyURL(a, cfg)
		require.NoError(t, err)
		resp, err := client.Do(req, selected, a.ID, 1)
		require.NoError(t, err, state)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Contains(t, string(body), "account-proxy")
	}
	// A live managed ingress must not become a fallback when the binding fails.
	bound.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL, nil)
	require.NoError(t, err)
	_, err = client.Do(req, p.URL(), a.ID, 1)
	require.Error(t, err)
	require.Equal(t, int64(3), boundCalls.Load())
	require.Zero(t, managedCalls.Load())
	require.Zero(t, directCalls.Load())
	require.Equal(t, int64(71), *a.ProxyID)
}

func TestClaudeEgressTransportRejectsMissingOrChangedBoundRoute(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	p := &service.Proxy{ID: 71, Protocol: "socks5", Host: "proxy.invalid", Port: 1080, Status: service.StatusActive}
	a := &service.Account{ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, ProxyID: &p.ID, Proxy: p}
	proxies := &strictEgressProxies{value: p}
	client, ok := strictEgressHTTPUpstream(cfg, &strictEgressAccounts{value: a}, proxies).(*httpUpstreamService)
	require.True(t, ok)
	req := httptest.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	for _, omitted := range []string{"", cfg.Gateway.ClaudeEgress.ProxyURL, "http://another.invalid:8080"} {
		_, err := client.resolveEgressForRequest(req, omitted, a.ID)
		require.ErrorIs(t, err, service.ErrAccountProxyUnavailable)
	}
	route, err := client.resolveEgressForRequest(req, p.URL(), a.ID)
	require.NoError(t, err)
	require.Equal(t, "socks5h://proxy.invalid:1080", route)
	mode, ok := client.egressModes.Load(a.ID)
	require.True(t, ok)
	require.Equal(t, "account_proxy:71", mode)
	// Even a still-hydrated account must not hide a failed repository lookup.
	proxies.err = errors.New("injected lookup failure")
	_, err = client.resolveEgressForRequest(req, p.URL(), a.ID)
	require.Error(t, err)
}

func TestClaudeEgressBoundProxyReceivesOfficialCONNECTHostname(t *testing.T) {
	var managedCalls, boundCalls atomic.Int64
	managed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		managedCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer managed.Close()
	connectTarget := make(chan string, 1)
	bound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		boundCalls.Add(1)
		if req.Method == http.MethodConnect {
			connectTarget <- req.Host
		}
		// Deliberately stop at the proxy: no official TLS connection is opened.
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bound.Close()
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: managed.URL}}}
	p := strictEgressBoundProxy(t, bound.URL)
	a := &service.Account{ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth, ProxyID: &p.ID, Proxy: p}
	client := strictEgressHTTPUpstream(cfg, &strictEgressAccounts{value: a}, &strictEgressProxies{value: p})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	require.NoError(t, err)
	_, err = client.Do(req, p.URL(), a.ID, 1)
	require.Error(t, err)
	require.Equal(t, int64(1), boundCalls.Load())
	require.Zero(t, managedCalls.Load())
	select {
	case target := <-connectTarget:
		require.Equal(t, "api.anthropic.com:443", target)
	default:
		t.Fatal("account proxy did not receive the official hostname via CONNECT")
	}
}

func TestClaudeEgressOAuthClientKeepsExplicitProxy(t *testing.T) {
	var managedCalls, boundCalls atomic.Int64
	managed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		managedCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer managed.Close()
	bound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		boundCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"uuid":"test-org"}]`)
	}))
	defer bound.Close()
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: managed.URL}}}
	client, ok := ProvideClaudeOAuthClient(cfg).(*claudeOAuthService)
	require.True(t, ok)
	client.baseURL = "http://claude.ai"
	org, err := client.GetOrganizationUUID(t.Context(), "simulated-session-key", bound.URL)
	require.NoError(t, err)
	require.Equal(t, "test-org", org)
	require.Equal(t, int64(1), boundCalls.Load())
	require.Zero(t, managedCalls.Load())
}

// Keep policy/transport tests offline: the production provider enables exit-IP
// probing, which must not create extra requests in the fake proxy counters.
func strictEgressHTTPUpstream(cfg *config.Config, accounts service.AccountRepository, proxies service.ProxyRepository) service.HTTPUpstream {
	s := ProvideHTTPUpstream(cfg, accounts, proxies).(*httpUpstreamService)
	s.audit = service.NewEgressAuditor("", nil)
	return s
}
