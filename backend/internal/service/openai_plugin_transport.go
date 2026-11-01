package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

func (s *OpenAIGatewayService) SetPluginManager(manager *PluginManager) {
	s.pluginManager = manager
}

// doOpenAIUpstream 只在 OpenAI OAuth 能力绑定已启用时把真实请求交给插件。
// 插件返回标准 http.Response，响应解析、错误映射、SSE 和计费仍由现有核心链处理。
func (s *OpenAIGatewayService) doOpenAIUpstream(request *http.Request, proxyURL string, account *Account) (*http.Response, error) {
	route, err := ResolveAccountProxyURL(account, s.cfg)
	if err != nil {
		return nil, err
	}
	if proxyURL != route {
		return nil, ErrAccountProxyUnavailable
	}
	route, err = ProxyRouteForTarget(s.cfg, request.URL.String(), route)
	if err != nil {
		return nil, err
	}
	proxyURL = route
	RecordAccountEgress(request.Context(), route)
	if s.pluginManager != nil {
		if account != nil && account.IsOpenAIOAuth() {
			currentRoute, err := resolveCurrentPluginEgress(request, s.accountRepo, account, proxyURL, s.cfg)
			if err != nil {
				return nil, err
			}
			proxyURL = currentRoute
		}
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(withEgressAuditor(request.Context(), s.httpUpstream), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// doOpenAIAccountTestUpstream 让 OpenAI OAuth 账号测试与真实转发使用同一插件路径。
// API Key 和未命中插件的账号保持各自原有的 HTTPUpstream 行为。
func (s *AccountTestService) doOpenAIAccountTestUpstream(
	request *http.Request,
	proxyURL string,
	account *Account,
	useTLSFallback bool,
) (*http.Response, error) {
	if s.pluginManager != nil {
		if account != nil && account.IsOpenAIOAuth() {
			currentRoute, err := resolveCurrentPluginEgress(request, s.accountRepo, account, proxyURL, s.cfg)
			if err != nil {
				return nil, err
			}
			proxyURL = currentRoute
		}
		response, handled, err := s.pluginManager.RoundTripOpenAIOAuth(withEgressAuditor(request.Context(), s.httpUpstream), request, proxyURL, account)
		if handled {
			return response, err
		}
	}
	if useTLSFallback {
		return s.httpUpstream.DoWithTLS(
			request,
			proxyURL,
			account.ID,
			account.Concurrency,
			s.tlsFPProfileService.ResolveTLSProfile(account),
		)
	}
	return s.httpUpstream.Do(request, proxyURL, account.ID, account.Concurrency)
}

// Plugin requests bypass HTTPUpstream, so check the freshly hydrated binding
// here as well. Validate eligible accounts even before a plugin binding is
// enabled, since the binding can change between this check and dispatch.
func resolveCurrentPluginEgress(request *http.Request, accounts AccountRepository, selected *Account, selectedProxy string, cfg *config.Config) (string, error) {
	if request == nil || request.URL == nil || accounts == nil || selected == nil {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	current, err := accounts.GetByID(request.Context(), selected.ID)
	if err != nil || current == nil || current.ID != selected.ID || !grokCredentialProxyIDsEqual(current.ProxyID, selected.ProxyID) {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	expected, err := ResolveAccountProxyURL(current, cfg)
	if err != nil {
		return "", err
	}
	actual, _, err := proxyurl.Parse(selectedProxy)
	if err != nil || actual != expected {
		return "", accountEgressUnavailable(ErrAccountProxyUnavailable)
	}
	return ProxyRouteForTarget(cfg, request.URL.String(), expected)
}
