package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type strictEgressProxyRepo struct {
	ProxyRepository
	value *Proxy
	err   error
}

func (r *strictEgressProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.value, r.err
}

func strictEgressAccount() *Account {
	id := int64(71)
	return &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id,
		Proxy:       &Proxy{ID: id, Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: StatusActive},
		Credentials: map[string]any{"refresh_token": "simulated-refresh-token"}}
}

func TestStrictEgressBrokenBindingsNeverBecomeDirect(t *testing.T) {
	for _, scenario := range []string{"missing", "mismatched", "disabled", "expired", "invalid-host", "invalid-port", "invalid-protocol"} {
		t.Run(scenario, func(t *testing.T) {
			a := strictEgressAccount()
			switch scenario {
			case "missing":
				a.Proxy = nil
			case "mismatched":
				a.Proxy.ID++
			case "disabled":
				a.Proxy.Status = "inactive"
			case "expired":
				past := time.Now().Add(-time.Minute)
				a.Proxy.ExpiresAt = &past
			case "invalid-host":
				a.Proxy.Host = ""
			case "invalid-port":
				a.Proxy.Port = 0
			case "invalid-protocol":
				a.Proxy.Protocol = "ftp"
			}
			route, err := ResolveAccountProxyURL(a)
			require.ErrorIs(t, err, ErrAccountProxyUnavailable)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.ShouldRetryNextAccount())
			require.False(t, failover.RetryableOnSameAccount)
			require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
			require.Empty(t, route)
			require.NotNil(t, a.ProxyID)
		})
	}
	_, err := ResolveAccountProxyFromRepository(t.Context(), &strictEgressProxyRepo{err: errors.New("simulated database outage")}, strictEgressAccount())
	require.ErrorIs(t, err, ErrAccountProxyUnavailable)
}

func TestStrictEgressUnboundAndThirdPartyRemainNormal(t *testing.T) {
	route, err := ResolveAccountProxyURL(&Account{Platform: PlatformOpenAI})
	require.NoError(t, err)
	require.Empty(t, route)
	a := strictEgressAccount()
	a.Platform = PlatformAnthropic
	a.Type = AccountTypeAPIKey
	a.Credentials["base_url"] = "https://third-party.invalid/v1"
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	route, err = ResolveAccountProxyURL(a, cfg)
	require.NoError(t, err)
	require.Equal(t, "http://proxy.invalid:8080", route)
}

func TestClaudeEgressOfficialPolicyAndConflict(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	a := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	route, err := ResolveAccountProxyURL(a, cfg)
	require.NoError(t, err)
	require.Equal(t, cfg.Gateway.ClaudeEgress.ProxyURL, route)
	id := int64(71)
	a.ProxyID = &id
	_, err = ResolveAccountProxyURL(a, cfg)
	require.Error(t, err)
	require.Error(t, ValidateAccountEgressSettings(a, cfg))
	_, err = ProxyRouteForTarget(cfg, "https://platform.claude.com/v1/oauth/token", "http://another.invalid:8080")
	require.Error(t, err)
	route, err = ProxyRouteForTarget(cfg, "https://api.openai.com/v1/responses", "")
	require.NoError(t, err)
	require.Empty(t, route)
}

type strictEgressOAuthClient struct {
	OpenAIOAuthClient
	calls int
	route string
}

func (c *strictEgressOAuthClient) RefreshTokenWithClientID(_ context.Context, _, route, _ string) (*openai.TokenResponse, error) {
	c.calls++
	c.route = route
	return &openai.TokenResponse{AccessToken: "simulated-access-token", RefreshToken: "simulated-refresh-token", ExpiresIn: 3600}, nil
}

func TestStrictEgressRefreshLookupFailureSendsNoRequest(t *testing.T) {
	client := &strictEgressOAuthClient{}
	svc := NewOpenAIOAuthService(&strictEgressProxyRepo{err: errors.New("injected lookup failure")}, client)
	_, err := svc.RefreshAccountToken(t.Context(), strictEgressAccount())
	require.Error(t, err)
	require.Zero(t, client.calls)
}

func TestStrictEgressOAuthSessionCannotChangeProxy(t *testing.T) {
	a := strictEgressAccount()
	repo := &strictEgressProxyRepo{value: a.Proxy}
	route, err := resolveOAuthSessionProxy(t.Context(), repo, a.ProxyID, nil, a.Proxy.URL())
	require.NoError(t, err)
	require.Equal(t, a.Proxy.URL(), route)
	other := int64(72)
	_, err = resolveOAuthSessionProxy(t.Context(), repo, a.ProxyID, &other, a.Proxy.URL())
	require.Error(t, err)
	repo.value.Host = "changed.invalid"
	_, err = resolveOAuthSessionProxy(t.Context(), repo, a.ProxyID, nil, route)
	require.Error(t, err)
}

func TestStrictEgressExpiryKeepsEveryLegacyBinding(t *testing.T) {
	for _, mode := range []string{FallbackModeNone, FallbackModeDirect, FallbackModeProxy} {
		a := strictEgressAccount()
		backup := int64(72)
		a.Proxy.FallbackMode = mode
		a.Proxy.BackupProxyID = &backup
		target, change := ResolveProxyFallbackTarget(*a.Proxy, map[int64]Proxy{backup: {ID: backup, Status: StatusActive}}, time.Now())
		require.Nil(t, target)
		require.False(t, change)
		require.Equal(t, int64(71), *a.ProxyID)
	}
}

type strictEgressWSDialer struct {
	calls  int
	routes []string
}

func (d *strictEgressWSDialer) Dial(_ context.Context, _ string, _ http.Header, route string) (openAIWSClientConn, int, http.Header, error) {
	d.calls++
	d.routes = append(d.routes, route)
	return nil, 0, nil, errors.New("simulated proxy handshake failure")
}

func TestStrictEgressWebSocketNeverDialsOnMissingBinding(t *testing.T) {
	dialer := &strictEgressWSDialer{}
	pool := &openAIWSConnPool{clientDialer: dialer}
	a := strictEgressAccount()
	a.Proxy = nil
	_, err := pool.Acquire(t.Context(), openAIWSAcquireRequest{Account: a, WSURL: "wss://upstream.invalid/ws"})
	require.Error(t, err)
	require.Zero(t, dialer.calls)
}

func TestStrictEgressWebSocketHandshakeFailureDoesNotRetryDirect(t *testing.T) {
	dialer := &strictEgressWSDialer{}
	pool := &openAIWSConnPool{clientDialer: dialer}
	a := strictEgressAccount()
	_, err := pool.dialConn(t.Context(), openAIWSAcquireRequest{Account: a, ProxyURL: a.Proxy.URL(), WSURL: "wss://upstream.invalid/ws"})
	require.Error(t, err)
	require.Equal(t, 1, dialer.calls)
}

func TestStrictEgressDetachedMediaRetainsFinalAccountRoute(t *testing.T) {
	ctx, cancel := context.WithCancel(WithAccountEgressCapture(t.Context()))
	RecordAccountEgress(ctx, "http://first.invalid:8080")
	RecordAccountEgress(ctx, "http://last.invalid:8080")
	cancel()
	detached := DetachedAccountEgress(ctx)
	require.NoError(t, detached.Err())
	req, err := http.NewRequestWithContext(detached, http.MethodGet, "https://media.invalid/image.png", nil)
	require.NoError(t, err)
	proxy, err := accountEgressProxy(req)
	require.NoError(t, err)
	require.Equal(t, "last.invalid:8080", proxy.Host)
	req = req.WithContext(DetachedAccountEgress(context.Background()))
	_, err = accountEgressProxy(req)
	require.ErrorIs(t, err, ErrAccountProxyUnavailable)
}

func TestStrictEgressBatchContextRejectsBrokenBinding(t *testing.T) {
	a := strictEgressAccount()
	ctx, err := WithAccountEgress(t.Context(), a)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://batch.invalid/result", nil)
	require.NoError(t, err)
	proxy, err := accountEgressProxy(req)
	require.NoError(t, err)
	require.Equal(t, "proxy.invalid:8080", proxy.Host)
	a.Proxy = nil
	_, err = WithAccountEgress(t.Context(), a)
	require.Error(t, err)
}

func TestStrictEgressProxyFallbackAPIsRejectAlternateRoutes(t *testing.T) {
	svc := &adminServiceImpl{}
	for _, mode := range []string{FallbackModeDirect, FallbackModeProxy} {
		_, err := svc.CreateProxy(t.Context(), &CreateProxyInput{FallbackMode: mode})
		require.Error(t, err)
		_, err = svc.UpdateProxy(t.Context(), 1, &UpdateProxyInput{FallbackMode: mode})
		require.Error(t, err)
	}
}

func TestStrictEgressWebSocketPreservesValidSOCKSBindingAcrossAcquireAndDial(t *testing.T) {
	for _, method := range []string{"acquire", "dial"} {
		t.Run(method, func(t *testing.T) {
			dialer := &strictEgressWSDialer{}
			pool := newOpenAIWSConnPool(&config.Config{})
			defer pool.Close()
			pool.setClientDialerForTest(dialer)
			a := strictEgressAccount()
			a.Proxy.Protocol = "socks5"
			req := openAIWSAcquireRequest{Account: a, ProxyURL: a.Proxy.URL(), WSURL: "wss://upstream.invalid/ws"}
			var err error
			if method == "acquire" {
				_, err = pool.Acquire(t.Context(), req)
			} else {
				_, err = pool.dialConn(t.Context(), req)
			}
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrAccountProxyUnavailable)
			require.NotEmpty(t, dialer.routes)
			for _, route := range dialer.routes {
				require.Equal(t, "socks5h://proxy.invalid:8080", route)
			}
			require.Equal(t, int64(71), *a.ProxyID)
		})
	}
}

func TestStrictEgressPolicyConflictsRetainCauseAndAllowNextAccount(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{Enabled: true, ProxyURL: "http://127.0.0.1:7890"}}}
	a := strictEgressAccount()
	a.Platform = PlatformAnthropic
	_, err := ResolveAccountProxyURL(a, cfg)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.True(t, failover.ShouldRetryNextAccount())
	var original *infraerrors.ApplicationError
	require.ErrorAs(t, err, &original)
	require.Equal(t, int32(http.StatusBadRequest), original.Code)
	// Admin validation remains a configuration error, rather than an inference retry.
	err = ValidateAccountEgressSettings(a, cfg)
	require.ErrorAs(t, err, &original)
	require.False(t, errors.As(err, &failover))
}

type strictEgressSuccessfulWSDialer struct {
	routes []string
}

func (d *strictEgressSuccessfulWSDialer) Dial(_ context.Context, _ string, _ http.Header, route string) (openAIWSClientConn, int, http.Header, error) {
	d.routes = append(d.routes, route)
	return &openAIWSFakeConn{}, 0, nil, nil
}

func TestStrictEgressWebSocketPoolNeverReusesAnotherExit(t *testing.T) {
	for _, scenario := range []string{"bind", "rebind", "endpoint", "credentials", "unbind", "same SOCKS route"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			dialer := &strictEgressSuccessfulWSDialer{}
			pool.setClientDialerForTest(dialer)
			before, after := strictEgressAccount(), strictEgressAccount()
			switch scenario {
			case "bind":
				before.ProxyID, before.Proxy = nil, nil
			case "rebind":
				id := int64(72)
				after.ProxyID, after.Proxy.ID = &id, id
				after.Proxy.Host = "second.invalid"
			case "endpoint":
				after.Proxy.Port++
			case "credentials":
				after.Proxy.Username, after.Proxy.Password = "new-user", "new-password"
			case "unbind":
				after.ProxyID, after.Proxy = nil, nil
			case "same SOCKS route":
				before.Proxy.Protocol, after.Proxy.Protocol = "socks5", "socks5h"
			}
			request := func(account *Account) openAIWSAcquireRequest {
				route := ""
				if account.Proxy != nil {
					route = account.Proxy.URL()
				}
				return openAIWSAcquireRequest{Account: account, ProxyURL: route, WSURL: "wss://upstream.invalid/ws"}
			}
			first, err := pool.Acquire(t.Context(), request(before))
			require.NoError(t, err)
			oldConn := first.conn
			first.Release()
			// Even an advisory preference must not select a connection through the old exit.
			next := request(after)
			next.PreferredConnID = first.ConnID()
			second, err := pool.Acquire(t.Context(), next)
			require.NoError(t, err)
			t.Cleanup(second.Release)
			if scenario == "same SOCKS route" {
				require.True(t, second.Reused())
				require.Equal(t, first.ConnID(), second.ConnID())
				require.Equal(t, []string{"socks5h://proxy.invalid:8080"}, dialer.routes)
			} else {
				require.False(t, second.Reused())
				require.NotEqual(t, first.ConnID(), second.ConnID())
				require.True(t, oldConn.isClosed())
				expected, err := ResolveAccountProxyURL(after)
				require.NoError(t, err)
				require.Len(t, dialer.routes, 2)
				require.Equal(t, expected, dialer.routes[1])
			}
		})
	}
}

func TestStrictEgressWebSocketForcedPreferredExitMismatchIsRejected(t *testing.T) {
	pool := newOpenAIWSConnPool(&config.Config{})
	t.Cleanup(pool.Close)
	dialer := &strictEgressSuccessfulWSDialer{}
	pool.setClientDialerForTest(dialer)
	a := strictEgressAccount()
	request := openAIWSAcquireRequest{Account: a, ProxyURL: a.Proxy.URL(), WSURL: "wss://upstream.invalid/ws"}
	first, err := pool.Acquire(t.Context(), request)
	require.NoError(t, err)
	first.Release()
	a.Proxy.Port++
	request.ProxyURL = a.Proxy.URL()
	request.PreferredConnID, request.ForcePreferredConn = first.ConnID(), true
	_, err = pool.Acquire(t.Context(), request)
	require.ErrorIs(t, err, errOpenAIWSPreferredConnUnavailable)
	require.Len(t, dialer.routes, 1)
}

type strictEgressChangingWSAccounts struct {
	AccountRepository
	before, after *Account
	reads         int
}

func (r *strictEgressChangingWSAccounts) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	if r.reads <= 2 {
		return r.before, nil
	}
	return r.after, nil
}

func TestStrictEgressWebSocketRechecksBindingBeforeReturningLease(t *testing.T) {
	before, after := strictEgressAccount(), strictEgressAccount()
	after.Proxy.Port++
	pool := newOpenAIWSConnPool(&config.Config{})
	t.Cleanup(pool.Close)
	pool.egressAccounts = &strictEgressChangingWSAccounts{before: before, after: after}
	dialer := &strictEgressSuccessfulWSDialer{}
	pool.setClientDialerForTest(dialer)
	lease, err := pool.Acquire(t.Context(), openAIWSAcquireRequest{Account: before, ProxyURL: before.Proxy.URL(), WSURL: "wss://upstream.invalid/ws"})
	require.ErrorIs(t, err, ErrAccountProxyUnavailable)
	require.Nil(t, lease)
	require.Len(t, dialer.routes, 1)
	// A rejected lease must release its token so the updated request can proceed.
	lease, err = pool.Acquire(t.Context(), openAIWSAcquireRequest{Account: after, ProxyURL: after.Proxy.URL(), WSURL: "wss://upstream.invalid/ws"})
	require.NoError(t, err)
	t.Cleanup(lease.Release)
	require.False(t, lease.Reused())
}
