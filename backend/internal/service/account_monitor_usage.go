package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
)

// Monitor reads have their own route-scoped outer cache. They must not join UI
// singleflights that can lose the route or issue a model fallback/credit query.
func (s *AccountUsageService) getMonitorUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	switch {
	case account.IsAnthropicOAuthOrSetupToken():
		if account.Type == AccountTypeSetupToken || account.IsSyntheticUITest() {
			return s.getPassiveUsageForAccount(ctx, account)
		}
		raw, err := s.fetchOAuthUsageRaw(ctx, account)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		usage := s.buildUsageInfo(raw, &now)
		usage.UpdatedAt = &now
		usage.Source = "active"
		if usage.SevenDayFable == nil {
			usage.SevenDayFable = buildPassiveUsageWindow(account.Extra, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset")
			setPassiveWindowSample(usage.SevenDayFable, account.Extra, "7d_oi")
		}
		return usage, nil
	case account.Platform == PlatformAntigravity:
		if s.antigravityQuotaFetcher == nil {
			return nil, ErrChannelMonitorAccountNotSupportable
		}
		policy, _ := monitorrequest.From(ctx)
		result, err := s.antigravityQuotaFetcher.FetchQuota(ctx, account, policy.Route)
		if err != nil {
			return nil, err
		}
		return result.UsageInfo, nil
	case account.IsOpenAIOAuth():
		if s.openAIQuotaService == nil {
			return nil, ErrChannelMonitorAccountNotSupportable
		}
		quota, err := s.openAIQuotaService.QueryUsage(withOpenAIQuotaUsageOnly(ctx), account.ID)
		if err != nil {
			return nil, err
		}
		updates := openAIQuotaDisplayUpdates(account, quota)
		if !hasOpenAIQuotaDisplayWindows(updates) {
			return nil, ErrChannelMonitorAccountNotSupportable
		}
		now := time.Now()
        usage := openAIUsageFromExtra(updates, now)
        usage.FiveHour = buildObservedCodexUsageProgress(updates, "5h", now)
        usage.SevenDay = buildObservedCodexUsageProgress(updates, "7d", now)
        usage.Source = "active"
        return usage, nil
	default:
		return s.getUsageForAccount(ctx, account, false)
	}
}

// monitorUsageMode is deliberately narrow: other providers retain their adapters,
// with the same proxy-pinned context propagated to every transport.
func monitorDedicatedUsage(ctx context.Context, account *Account) bool {
	return monitorrequest.Active(ctx) && (account.IsAnthropicOAuthOrSetupToken() || account.IsOpenAIOAuth() || account.Platform == PlatformAntigravity)
}

// GetMonitorUsageForAccount keeps quota monitoring independent of page refresh
// policy even for callers without an active transport policy (e.g. unit stubs).
func (s *AccountUsageService) GetMonitorUsageForAccount(ctx context.Context, account *Account) (*UsageInfo, error) {
 if account == nil { return nil, ErrAccountNotFound }
 if err := scopedAccountAccess(ctx, s.accountRepo, account, "accounts.usage.read", true); err != nil { return nil, err }
 usage, err := s.getMonitorUsage(ctx, account)
 return s.attachQuotaEstimates(ctx, account, usage), err
}
