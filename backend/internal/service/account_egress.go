package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claudedomain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

var ErrAccountProxyUnavailable = errors.New("account proxy unavailable; egress is blocked")

// Local egress failures must take the same retry-next-account path as transport
// failures, while the selected account remains blocked and its binding retained.
func accountEgressUnavailable(cause error) *UpstreamFailoverError {
	return &UpstreamFailoverError{
		StatusCode:        http.StatusServiceUnavailable,
		ResponseBody:      []byte(`{"error":{"type":"upstream_error","message":"Account egress is unavailable"}}`),
		Reason:            GatewayFailureReason("account_egress_unavailable"),
		NextAccountAction: NextAccountRetry,
		Cause:             cause,
	}
}

// ResolveAccountProxyURL never treats a broken binding as an unbound account.
// An explicit binding takes priority over the managed Claude default, including
// when that binding is unavailable. Callers must propagate errors before dialing.
func ResolveAccountProxyURL(account *Account, configs ...*config.Config) (string, error) {
	if account == nil {
		return "", accountEgressUnavailable(fmt.Errorf("%w: account missing", ErrAccountProxyUnavailable))
	}
	if account.ProxyID != nil {
		return validatedBoundProxyURL(account.ProxyID, account.Proxy)
	}
	var cfg *config.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	if cfg != nil && cfg.Gateway.ClaudeEgress.Enabled && IsOfficialClaudeAccount(account) {
		route, err := cfg.Gateway.ClaudeEgress.ValidatedProxyURL()
		if err != nil {
			return "", accountEgressUnavailable(err)
		}
		return route, nil
	}
	return "", nil
}

func validatedBoundProxyURL(id *int64, proxy *Proxy) (string, error) {
	if id == nil {
		return "", nil
	}
	if *id <= 0 || proxy == nil || proxy.ID != *id || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	if strings.TrimSpace(proxy.Host) == "" || proxy.Port < 1 || proxy.Port > 65535 {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	value, _, err := proxyurl.Parse(proxy.URL())
	if err != nil || value == "" {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	return value, nil
}

// ResolveProxyBinding is also used before an account exists (OAuth/import flows).
// A repository error must never become an empty proxy URL.
func ResolveProxyBinding(ctx context.Context, repo ProxyRepository, id *int64) (string, error) {
	if id == nil {
		return "", nil
	}
	if repo == nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	proxy, err := repo.GetByID(ctx, *id)
	if err != nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	return validatedBoundProxyURL(id, proxy)
}

func ResolveAccountProxyFromRepository(ctx context.Context, repo ProxyRepository, account *Account, configs ...*config.Config) (string, error) {
	if account == nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	if account.ProxyID == nil {
		return ResolveAccountProxyURL(account, configs...)
	}
	// Validate the binding even when a caller supplied a stale hydrated Proxy.
	copy := *account
	if repo == nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	proxy, err := repo.GetByID(ctx, *account.ProxyID)
	if err != nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	copy.Proxy = proxy
	return ResolveAccountProxyURL(&copy, configs...)
}

func IsOfficialClaudeAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformAnthropic || account.IsBedrock() || account.Type == AccountTypeServiceAccount {
		return false
	}
	if account.IsCustomBaseURLEnabled() && strings.TrimSpace(account.GetCustomBaseURL()) != "" {
		return claudedomain.ContainsURL(account.GetCustomBaseURL())
	}
	if raw := strings.TrimSpace(account.GetCredential("base_url")); raw != "" {
		return claudedomain.ContainsURL(raw)
	}
	return true
}

func (s *OAuthService) resolveClaudeProxy(ctx context.Context, id *int64) (string, error) {
	return ResolveAccountProxyFromRepository(ctx, s.proxyRepo, &Account{Platform: PlatformAnthropic, ProxyID: id}, s.cfg)
}

// A session is pinned at creation. Changing a binding or its endpoint requires
// a new authorization session; a failed lookup cannot reuse a stale URL.
func resolveOAuthSessionProxy(ctx context.Context, repo ProxyRepository, sessionID, inputID *int64, snapshot string, configs ...*config.Config) (string, error) {
	if inputID != nil && (sessionID == nil || *inputID != *sessionID) {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	route, err := ResolveAccountProxyFromRepository(ctx, repo, &Account{Platform: PlatformAnthropic, ProxyID: sessionID}, configs...)
	if err != nil || route != snapshot {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	return route, nil
}

// ProxyRouteForTarget preserves an explicitly selected proxy for every target.
// Only an unselected official Claude request uses the managed Mihomo default.
// Account-aware callers must validate the current binding before calling this.
func ProxyRouteForTarget(cfg *config.Config, target, selectedProxy string) (string, error) {
	if cfg == nil || !cfg.Gateway.ClaudeEgress.Enabled || !claudedomain.ContainsURL(target) {
		return selectedProxy, nil
	}
	if selectedProxy != "" {
		route, _, err := proxyurl.Parse(selectedProxy)
		if err != nil || route == "" {
			return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
		}
		return route, nil
	}
	return cfg.Gateway.ClaudeEgress.ValidatedProxyURL()
}
