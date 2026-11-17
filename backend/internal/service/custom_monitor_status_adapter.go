package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// Status adaptation is kept together without altering execution, route pinning,
// history persistence or the scheduler's account eligibility rules.
// monitorQuotaWindowStatus 只按全局 5h/7d 窗口判定；其他窗口和余额仅供展示。
func monitorQuotaWindowStatus(snapshot *domain.MonitorQuotaSnapshot) (string, string) {
	status, message := MonitorStatusError, "global 5h/7d quota unavailable"
	if snapshot == nil || !snapshot.Success {
		return status, message
	}
	for _, tier := range snapshot.Tiers {
		if tier.Label != "" || (tier.Window != "5h" && tier.Window != "7d") {
			continue
		}
		if status == MonitorStatusError {
			status, message = MonitorStatusOperational, ""
		}
		if tier.WindowInactive {
			continue
		}
		if tier.UsedPercent >= 100 {
			return MonitorStatusFailed, "quota exhausted: " + tier.Window
		}
		if tier.UsedPercent >= monitorQuotaDegradedUsedPercent {
			status = MonitorStatusDegraded
			message = fmt.Sprintf("quota high: %s at %.1f%%", tier.Window, tier.UsedPercent)
		}
	}
	return status, message
}

// monitorAccountStatus 复用调度器判定；暂停和未知仅供当前界面展示。
func monitorAccountStatus(account *Account, model string) (string, string) {
	if account == nil {
		return MonitorStatusUnknown, "linked account unavailable"
	}
	if account.Status == StatusError {
		return MonitorStatusFailed, "linked account is in error state"
	}
	if account.Status == StatusDisabled {
		return MonitorStatusPaused, "linked account is disabled"
	}
	// 到期任务会自动关闭 Schedulable，需与人工停调区分。
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !time.Now().Before(*account.ExpiresAt) {
		return MonitorStatusFailed, "linked account expired"
	}
	if !account.Schedulable {
		return MonitorStatusPaused, "linked account scheduling is paused"
	}
	if !account.IsSchedulable() {
		return MonitorStatusFailed, "linked account unavailable for scheduling (expired, quota exhausted or temporarily unschedulable)"
	}
	if model != "" && model != MonitorDefaultQuotaModel && account.isModelRateLimitedWithContext(context.Background(), model) {
		return MonitorStatusFailed, "linked account temporarily unschedulable for this model"
	}
	return "", ""
}

func (s *ChannelMonitorService) loadMonitorAccount(ctx context.Context, m *ChannelMonitor) *Account {
	if m.AccountID == nil || s.quotaFetcher == nil {
		return nil
	}
	account, err := s.quotaFetcher.LoadAccount(ctx, *m.AccountID)
	if err != nil || account == nil {
		slog.Warn("channel_monitor: load linked account failed", "monitor_id", m.ID, "error", err)
		return nil
	}
	return account
}

// applyLinkedAccountSchedulingStatus 绕过配额缓存，历史只保存原四态。
func (s *ChannelMonitorService) applyLinkedAccountSchedulingStatus(ctx context.Context, m *ChannelMonitor, results []*CheckResult) {
	if m.AccountID == nil {
		return
	}
	account := s.loadMonitorAccount(ctx, m)
	if account != nil && account.Platform != m.Provider {
		account = nil
	}
	for _, res := range results {
		if res.Status != MonitorStatusOperational && res.Status != MonitorStatusDegraded {
			continue
		}
		status, message := monitorAccountStatus(account, res.Model)
		if status == MonitorStatusUnknown {
			status = MonitorStatusError
		}
		if status == MonitorStatusFailed || status == MonitorStatusError {
			res.Status, res.Message = status, message
		}
	}
}

// monitorDisplayStatus 共用于管理列表、用户卡片和详情，不改写历史样本。
func (s *ChannelMonitorService) monitorDisplayStatus(ctx context.Context, m *ChannelMonitor, accounts map[int64]*Account) func(string, *ChannelMonitorLatest) string {
	var account *Account
	if m.AccountID != nil {
		var loaded bool
		account, loaded = accounts[*m.AccountID]
		if !loaded {
			account = s.loadMonitorAccount(ctx, m)
			accounts[*m.AccountID] = account // 失败也只读取一次。
		}
		if account != nil && account.Platform != m.Provider {
			account = nil
		}
	}
	_, _, routeErr := s.monitorRouteSnapshot(ctx, m, account)
	m.EgressConfigured = routeErr == nil
	return func(model string, latest *ChannelMonitorLatest) string {
		// 自动检测开关仅控制定时采集，不代表关联账号暂停调度。
		if !m.Enabled {
			return MonitorStatusPaused
		}
		if m.AccountID != nil {
			if status, _ := monitorAccountStatus(account, model); status != "" {
				return status
			}
		}
		if (m.AccountID == nil && defaultCheckMode(m.CheckMode) == MonitorCheckModeQuota) ||
			routeErr != nil || (defaultCheckMode(m.CheckMode) != MonitorCheckModeQuota && m.APIKeyDecryptFailed) || latest == nil {
			return MonitorStatusUnknown
		}
		maxAge := 12 * time.Hour
		if m.AutoCheckEnabled {
			maxAge = time.Duration(2*(m.IntervalSeconds+m.JitterSeconds))*time.Second + 90*time.Second
		}
		if latest.CheckedAt.IsZero() || time.Since(latest.CheckedAt) > maxAge || latest.Status == MonitorStatusError {
			return MonitorStatusUnknown
		}
		return latest.Status
	}
}
