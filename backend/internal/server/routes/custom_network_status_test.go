package routes

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestNetworkStatusGatewayAuthenticationAndSecretBoundary(t *testing.T) {
	token := strings.Repeat("x", 40)
	calls := 0
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		require.Empty(t, r.Header.Get("X-API-Key"))
		require.Equal(t, "/history?days=7&id=0", r.URL.String())
		_, _ = io.WriteString(w, `{"code":0,"data":{"runs":[]}}`)
	}))
	defer worker.Close()
	t.Setenv("NETWORK_STATUS_URL", worker.URL)
	t.Setenv("NETWORK_STATUS_TOKEN", token)
	gateway := newNetworkStatusGateway()
	router := gin.New()
	group := router.Group("/api/v1")
	group.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer browser-session" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	group.GET("/network-status/routes/:id/history", gateway.forward("/history"))
	url := "/api/v1/network-status/routes/0/history?days=7&token=browser-secret&target=https://evil.invalid"
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	require.Zero(t, calls)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer browser-session")
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("X-API-Key", "browser-api-key")
	result := httptest.NewRecorder()
	router.ServeHTTP(result, req)
	require.Equal(t, http.StatusOK, result.Code)
	require.Equal(t, "no-store", result.Header().Get("Cache-Control"))
	require.Equal(t, 1, calls)
}

func TestNetworkStatusGatewayDoesNotFollowRedirectsOrReturnPrivateErrors(t *testing.T) {
	redirected := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected = true }))
	defer other.Close()
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/secret", http.StatusTemporaryRedirect)
	}))
	defer worker.Close()
	t.Setenv("NETWORK_STATUS_URL", worker.URL)
	t.Setenv("NETWORK_STATUS_TOKEN", strings.Repeat("x", 40))
	router := gin.New()
	router.GET("/snapshot", newNetworkStatusGateway().forward("/snapshot"))
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/snapshot", nil))
	require.Equal(t, http.StatusServiceUnavailable, result.Code)
	require.False(t, redirected)
	require.NotContains(t, result.Body.String(), "/secret")
	require.NotContains(t, result.Body.String(), other.URL)
}

func TestNetworkStatusGatewayValidatesInputsAndUnavailableConfiguration(t *testing.T) {
	for _, endpoint := range []string{"", "file:///private", "http://user:password@localhost", "http://localhost?token=secret"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("NETWORK_STATUS_URL", endpoint)
			t.Setenv("NETWORK_STATUS_TOKEN", strings.Repeat("x", 40))
			router := gin.New()
			registerNetworkStatusUserRoutes(router.Group(""))
			result := httptest.NewRecorder()
			router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/network-status/snapshot", nil))
			require.Equal(t, http.StatusServiceUnavailable, result.Code)
			require.NotContains(t, result.Body.String(), "password")
		})
	}
	t.Setenv("NETWORK_STATUS_URL", "http://127.0.0.1:1")
	t.Setenv("NETWORK_STATUS_TOKEN", strings.Repeat("x", 40))
	router := gin.New()
	registerNetworkStatusUserRoutes(router.Group(""))
	for _, path := range []string{"/network-status/snapshot?days=999", "/network-status/routes/-1/history", "/network-status/routes/not-an-id/history"} {
		result := httptest.NewRecorder()
		router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusBadRequest, result.Code)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/network-status/run", nil))
	require.Equal(t, http.StatusNotFound, result.Code)
}
