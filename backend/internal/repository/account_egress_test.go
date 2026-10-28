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
	client := ProvideHTTPUpstream(nil, accounts, proxies)
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
	client := ProvideHTTPUpstream(nil, &strictEgressAccounts{value: a}, proxies)
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

func TestClaudeEgressTransportSelectsOnlyManagedIngress(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	a := &service.Account{ID: 4, Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth}
	client := ProvideHTTPUpstream(cfg, &strictEgressAccounts{value: a}, &strictEgressProxies{}).(*httpUpstreamService)
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
	defer db.Close()
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
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	r := &proxyRepository{client: client}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM proxies.*FOR UPDATE").WithArgs(int64(71)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(71))
	mock.ExpectQuery("SELECT COUNT.*FROM accounts").WithArgs(int64(71)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	require.ErrorIs(t, r.Delete(t.Context(), 71), service.ErrProxyInUse)
	require.NoError(t, mock.ExpectationsWereMet())
}
