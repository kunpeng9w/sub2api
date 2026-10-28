package service

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type egressReviewAccountRepo struct {
	AccountRepository
	account *Account
	err     error
	reads   int
}

func (r *egressReviewAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, r.err
}

func TestStrictEgressPluginChecksCurrentBindingBeforeDispatch(t *testing.T) {
	for _, entry := range []string{"gateway", "account test TLS", "account test HTTP"} {
		for _, scenario := range []string{
			"unchanged", "direct", "socks", "lookup failure", "missing account", "missing repository",
			"bind", "unbind", "rebind", "disabled", "expired", "missing proxy", "endpoint", "credentials",
		} {
			t.Run(entry+"/"+scenario, func(t *testing.T) {
				selected, current := strictEgressAccount(), strictEgressAccount()
				repo := &egressReviewAccountRepo{account: current}
				var accounts AccountRepository = repo
				allowed := false
				switch scenario {
				case "unchanged":
					allowed = true
				case "direct":
					selected.ProxyID, selected.Proxy = nil, nil
					current.ProxyID, current.Proxy = nil, nil
					allowed = true
				case "socks":
					selected.Proxy.Protocol, current.Proxy.Protocol = "socks5", "socks5h"
					allowed = true
				case "lookup failure":
					repo.err = errors.New("injected lookup failure")
				case "missing account":
					repo.account = nil
				case "missing repository":
					accounts = nil
				case "bind":
					selected.ProxyID, selected.Proxy = nil, nil
				case "unbind":
					current.ProxyID, current.Proxy = nil, nil
				case "rebind":
					id := int64(72)
					current.ProxyID, current.Proxy.ID = &id, id
				case "disabled":
					current.Proxy.Status = "inactive"
				case "expired":
					past := time.Now().Add(-time.Minute)
					current.Proxy.ExpiresAt = &past
				case "missing proxy":
					current.Proxy = nil
				case "endpoint":
					current.Proxy.Port++
				case "credentials":
					// Passwords are only sent when the proxy has a username.
					selected.Proxy.Username, current.Proxy.Username = "proxy-user", "proxy-user"
					selected.Proxy.Password = "old-password"
					current.Proxy.Password = "changed-password"
				}
				manager := &PluginManager{}
				// Reaching this sentinel means dispatch was permitted. No plugin
				// process or network connection is needed for this regression.
				manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100, unavailable: "dispatch sentinel"})
				upstream := &pluginRoutingHTTPUpstream{}
				request := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", nil)
				route, err := ResolveAccountProxyURL(selected)
				require.NoError(t, err)
				var response *http.Response
				if entry == "gateway" {
					svc := &OpenAIGatewayService{accountRepo: accounts, pluginManager: manager, httpUpstream: upstream}
					response, err = svc.doOpenAIUpstream(request, route, selected)
				} else {
					svc := &AccountTestService{accountRepo: accounts, pluginManager: manager, httpUpstream: upstream}
					response, err = svc.doOpenAIAccountTestUpstream(request, route, selected, entry == "account test TLS")
				}
				require.Nil(t, response)
				require.Error(t, err)
				if allowed {
					require.ErrorContains(t, err, "dispatch sentinel")
				} else {
					require.ErrorIs(t, err, ErrAccountProxyUnavailable)
					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.True(t, failover.ShouldRetryNextAccount())
				}
				if accounts != nil {
					require.Equal(t, 1, repo.reads)
				}
				require.Zero(t, upstream.doCalls)
				require.Zero(t, upstream.doWithTLSCalls)
			})
		}
	}
}

func TestStrictEgressCountTokensWritesProtocolError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"anthropic", "anthropic passthrough", "openai native", "openai anthropic"} {
		for _, failure := range []string{"missing", "disabled", "expired"} {
			t.Run(endpoint+"/"+failure, func(t *testing.T) {
				account := strictEgressAccount()
				account.Type = AccountTypeAPIKey
				account.Credentials = map[string]any{"api_key": "test-key"}
				switch failure {
				case "missing":
					account.Proxy = nil
				case "disabled":
					account.Proxy.Status = "inactive"
				case "expired":
					past := time.Now().Add(-time.Minute)
					account.Proxy.ExpiresAt = &past
				}
				body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello"}]}`)
				if endpoint == "openai native" {
					body = []byte(`{"model":"gpt-5.4","input":"hello"}`)
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/count_tokens", bytes.NewReader(body))
				upstream := &pluginRoutingHTTPUpstream{}
				cfg := &config.Config{}
				var err error
				if strings.HasPrefix(endpoint, "anthropic") {
					account.Platform = PlatformAnthropic
					account.Extra = map[string]any{"anthropic_passthrough": endpoint == "anthropic passthrough"}
					svc := &GatewayService{cfg: cfg, httpUpstream: upstream}
					parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6"}
					err = svc.ForwardCountTokens(c.Request.Context(), c, account, parsed)
				} else {
					svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
					if endpoint == "openai native" {
						err = svc.ForwardResponsesInputTokens(c.Request.Context(), c, account, body)
					} else {
						err = svc.ForwardCountTokensAsAnthropic(c.Request.Context(), c, account, body, "gpt-5.4")
					}
				}
				require.ErrorIs(t, err, ErrAccountProxyUnavailable)
				require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
				require.Equal(t, "upstream_error", gjson.Get(recorder.Body.String(), "error.type").String())
				require.Equal(t, "Account egress is unavailable", gjson.Get(recorder.Body.String(), "error.message").String())
				if endpoint != "openai native" {
					require.Equal(t, "error", gjson.Get(recorder.Body.String(), "type").String())
				}
				require.Zero(t, upstream.doCalls)
				require.Zero(t, upstream.doWithTLSCalls)
			})
		}
	}
}

func TestStrictEgressAccountTestsEmitTerminalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformTypeSafe} {
		t.Run(platform, func(t *testing.T) {
			account := strictEgressAccount()
			account.Platform, account.Type, account.Proxy = platform, AccountTypeAPIKey, nil
			account.Credentials = map[string]any{"api_key": "test-key"}
			repo := &egressReviewAccountRepo{account: account}
			upstream := &pluginRoutingHTTPUpstream{}
			svc := &AccountTestService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/4/test", nil)
			err := svc.TestAccountConnection(c, account.ID, "", "", AccountTestModeDefault)
			require.ErrorIs(t, err, ErrAccountProxyUnavailable)
			_, message := parseTestSSEOutput(recorder.Body.String())
			require.Equal(t, ErrAccountProxyUnavailable.Error(), message)
			require.Contains(t, recorder.Body.String(), `"type":"error"`)
			require.NotContains(t, recorder.Body.String(), `"success":true`)
			require.Zero(t, upstream.doCalls)
			require.Zero(t, upstream.doWithTLSCalls)
		})
	}
}

func TestStrictEgressGrokAccountTestsEmitTerminalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"responses", "image", "video", "video result", "search", "tts", "stt", "realtime"} {
		t.Run(mode, func(t *testing.T) {
			account := strictEgressAccount()
			account.Platform, account.Proxy = PlatformGrok, nil
			upstream := &pluginRoutingHTTPUpstream{}
			svc := &AccountTestService{cfg: &config.Config{}, httpUpstream: upstream}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/admin/accounts/4/test", nil)
			ctx := c.Request.Context()
			var err error
			switch mode {
			case "responses":
				err = svc.testGrokResponsesConnection(c, ctx, account, "test-token", "grok-4.3")
			case "image":
				err = svc.testGrokImageGeneration(c, ctx, account, "test-token", "", "", "")
			case "video":
				err = svc.testGrokVideoGeneration(c, ctx, account, "test-token", "", "", AccountTestOptions{})
			case "video result":
				err = svc.emitGrokVideoResult(c, ctx, account, "test-token", "test-request", nil)
			case "search":
				err = svc.testGrokWebSearch(c, ctx, account, "test-token", "test")
			case "tts":
				err = svc.testGrokTTS(c, ctx, account, "test-token", "test")
			case "stt":
				err = svc.testGrokSTT(c, ctx, account, "test-token", "")
			case "realtime":
				err = svc.testGrokRealtime(c, ctx, account, "test-token", "")
			}
			require.ErrorIs(t, err, ErrAccountProxyUnavailable)
			_, message := parseTestSSEOutput(recorder.Body.String())
			require.Equal(t, ErrAccountProxyUnavailable.Error(), message)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"error"`))
			require.Zero(t, upstream.doCalls)
			require.Zero(t, upstream.doWithTLSCalls)
		})
	}
}
