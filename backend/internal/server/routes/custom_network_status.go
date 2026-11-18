package routes

// This is the only backend integration with the independent network monitor.
// Inventory, credentials, rules, scheduling and history belong to that service.
import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type networkStatusGateway struct {
	endpoint string
	token    string
	client   *http.Client
}

func newNetworkStatusGateway() *networkStatusGateway {
	endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("NETWORK_STATUS_URL")), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		endpoint = ""
	}
	return &networkStatusGateway{
		endpoint: endpoint,
		token:    strings.TrimSpace(os.Getenv("NETWORK_STATUS_TOKEN")),
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy:                 nil,
				MaxIdleConns:          10,
				IdleConnTimeout:       60 * time.Second,
				ResponseHeaderTimeout: 8 * time.Second,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func registerNetworkStatusUserRoutes(authenticated *gin.RouterGroup) {
	gateway := newNetworkStatusGateway()
	group := authenticated.Group("/network-status")
	group.GET("/snapshot", gateway.forward("/snapshot"))
	group.GET("/routes/:id/history", gateway.forward("/history"))
	group.GET("/rules", gateway.forward("/rules"))
}

func registerNetworkStatusAdminRoutes(admin *gin.RouterGroup) {
	gateway := newNetworkStatusGateway()
	group := admin.Group("/network-status")
	// A monitor control permission is intentionally not implied by proxies.read.
	group.Use(func(c *gin.Context) {
		if adminaccess.RestrictedFromContext(c.Request.Context()) != nil {
			response.Forbidden(c, "Network monitor controls require a full administrator")
			c.Abort()
			return
		}
		c.Next()
	})
	group.PUT("/rules", gateway.forward("/rules"))
	group.POST("/run", gateway.forward("/run"))
}

func (g *networkStatusGateway) forward(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		unavailable := func() {
			response.ErrorWithDetails(c, http.StatusServiceUnavailable, "Network monitor is unavailable or not configured", "NETWORK_STATUS_UNAVAILABLE", nil)
		}
		if g.endpoint == "" || len(g.token) < 32 {
			unavailable()
			return
		}
		query := url.Values{}
		if path == "/snapshot" || path == "/history" {
			days := c.DefaultQuery("days", "7")
			if days != "1" && days != "7" && days != "30" {
				response.BadRequest(c, "days must be 1, 7 or 30")
				return
			}
			query.Set("days", days)
		}
		if path == "/history" {
			id, err := strconv.ParseInt(c.Param("id"), 10, 64)
			if err != nil || id < 0 {
				response.BadRequest(c, "Invalid route ID")
				return
			}
			query.Set("id", strconv.FormatInt(id, 10))
		}
		var body []byte
		if c.Request.Method != http.MethodGet {
			var err error
			body, err = io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 16*1024))
			if err != nil {
				response.BadRequest(c, "Request body is too large")
				return
			}
		}
		target := g.endpoint + path
		if len(query) > 0 {
			target += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, bytes.NewReader(body))
		if err != nil {
			unavailable()
			return
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		req.Header.Set("Content-Type", "application/json")
		res, err := g.client.Do(req)
		if err != nil {
			unavailable()
			return
		}
		defer func() { _ = res.Body.Close() }()
		const limit = 4 * 1024 * 1024
		payload, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
		if err != nil || len(payload) > limit || !json.Valid(payload) {
			unavailable()
			return
		}
		switch res.StatusCode {
		case http.StatusOK, http.StatusAccepted, http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests:
			c.Data(res.StatusCode, "application/json; charset=utf-8", payload)
		default:
			unavailable()
		}
	}
}
