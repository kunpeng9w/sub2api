package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMonitorUserQuotaVisibilityAndErrorRedaction(t *testing.T) {
	quota := &domain.MonitorQuotaSnapshot{Success: false, Error: "private upstream body with proxy or credential details", FetchedAt: time.Now()}
	view := &service.UserMonitorView{ID: 7, LatestQuota: quota, CanRun: true}
	hidden := userMonitorViewToItem(view, false)
	require.Nil(t, hidden.LatestQuota)
	visible := userMonitorViewToItem(view, true)
	require.Equal(t, "Quota data unavailable", visible.LatestQuota.Error)
	require.Contains(t, quota.Error, "private upstream body", "shared snapshot must remain unchanged")
	data, err := json.Marshal(visible)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private upstream body")
	require.NotContains(t, string(data), "proxy_id")
	require.NotContains(t, string(data), "account_id")
	quota.Success = true
	require.Empty(t, safeUserMonitorQuota(quota).Error, "successful snapshots must not leak provider warning bodies either")
}
