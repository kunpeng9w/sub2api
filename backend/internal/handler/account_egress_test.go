//go:build unit

package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStrictEgressGeminiNativeFailureRetriesNextAccountAndReturnsErrorWhenExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, scenario := range []string{"missing", "disabled", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", nil)
			proxyID := int64(71)
			account := &service.Account{ID: 4, Platform: service.PlatformGemini, Type: service.AccountTypeAPIKey,
				ProxyID: &proxyID, Proxy: &service.Proxy{ID: proxyID, Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: service.StatusActive}}
			switch scenario {
			case "missing":
				account.Proxy = nil
			case "disabled":
				account.Proxy.Status = "inactive"
			case "expired":
				past := time.Now().Add(-time.Minute)
				account.Proxy.ExpiresAt = &past
			}
			// No transport/token provider is installed: this must stop before any dial.
			result, err := (&service.GeminiMessagesCompatService{}).ForwardNative(c.Request.Context(), c, account,
				"gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
			require.Nil(t, result)
			require.ErrorIs(t, err, service.ErrAccountProxyUnavailable)
			var failover *service.UpstreamFailoverError
			require.True(t, errors.As(err, &failover))
			require.False(t, c.Writer.Written())
			state := NewFailoverState(1, false)
			require.Equal(t, FailoverContinue, state.HandleFailoverError(c.Request.Context(), nil, account.ID, account.Platform, 3, failover))
			require.Contains(t, state.FailedAccountIDs, account.ID)
			require.Empty(t, state.SameAccountRetryCount)
			require.Equal(t, proxyID, *account.ProxyID)
			// The same error at the retry limit must produce a Google JSON error, never an empty 200.
			require.Equal(t, FailoverExhausted, state.HandleFailoverError(c.Request.Context(), nil, 5, account.Platform, 3, failover))
			(&GatewayHandler{}).handleGeminiFailoverExhausted(c, state.LastFailoverErr)
			require.GreaterOrEqual(t, recorder.Code, http.StatusInternalServerError)
			require.NotEmpty(t, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), "error")
		})
	}
}
