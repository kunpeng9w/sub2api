package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Explicit model tests keep accepting quota response headers, including 429.
// This helper must not be used to send requests from the usage service.
func TestExtractOpenAIModelTestUsageUpdatesAccepts429WithCodexHeaders(t *testing.T) {
	t.Parallel()
	headers := make(http.Header)
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")
	updates, err := extractOpenAIModelTestUsageUpdates(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headers})
	require.NoError(t, err)
	require.Equal(t, 100.0, updates["codex_5h_used_percent"])
	require.Equal(t, 100.0, updates["codex_7d_used_percent"])
}
