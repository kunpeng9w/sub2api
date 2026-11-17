package service

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// These are local refresh-policy limits, independent of upstream snapshot parsing.
const openAIUsageCacheTTL = 10 * time.Minute

// One waiter may cancel without cancelling the bounded shared network task.
const openAIUsageRefreshTimeout = time.Minute

type openAIUsageCacheEntry struct {
	timestamp time.Time
	updates   map[string]any
	message   string
	errorCode string
}

func openAIUsageFromExtra(extra map[string]any, now time.Time) *UsageInfo {
	usage := &UsageInfo{}
	applyExtraToUsage(usage, extra, now)
	if usage.FiveHour != nil || usage.SevenDay != nil {
		if ts, err := parseTime(fmt.Sprint(extra["codex_usage_updated_at"])); err == nil && !ts.IsZero() {
			usage.UpdatedAt = &ts
		}
	}
	return usage
}

func (s *AccountUsageService) cachedOpenAIUsage(accountID int64) *openAIUsageCacheEntry {
	if s.cache != nil {
		if value, ok := s.cache.openAIUsageCache.Load(accountID); ok {
			entry, _ := value.(*openAIUsageCacheEntry)
			return entry
		}
	}
	return nil
}

// Successes describe the account, while failures describe a refresh policy.
// A read-only failure must not throttle a privileged model fallback.
func openAIUsageFlightKey(ctx context.Context, accountID int64) string {
	scope := "full"
	if adminaccess.RestrictedFromContext(ctx) != nil {
		scope = "read-only"
	}
	return fmt.Sprintf("usage:%d:%s", accountID, scope)
}

func (s *AccountUsageService) cachedOpenAIUsageForContext(ctx context.Context, accountID int64) *openAIUsageCacheEntry {
	success := s.cachedOpenAIUsage(accountID)
	if s.cache != nil {
		if value, ok := s.cache.openAIFailures.Load(openAIUsageFlightKey(ctx, accountID)); ok {
			failure, _ := value.(*openAIUsageCacheEntry)
			if failure != nil && (success == nil || !failure.timestamp.Before(success.timestamp)) {
				return failure
			}
		}
	}
	return success
}

// refreshOpenAIUsageSnapshot owns the local cache, permission-scoped singleflight
// and waiter cancellation policy. Monitor reads keep their separate lifecycle.
func (s *AccountUsageService) refreshOpenAIUsageSnapshot(ctx context.Context, account *Account, force bool) (*openAIUsageCacheEntry, error) {
	refresh := func(refreshCtx context.Context) *openAIUsageCacheEntry {
		if cached := s.cachedOpenAIUsageForContext(ctx, account.ID); !force && cached != nil && time.Since(cached.timestamp) < openAIUsageCacheTTL {
			return cached
		}
		result := s.queryOpenAIUsageSnapshot(refreshCtx, account)
		if s.cache != nil {
			if result.message != "" {
				s.cache.openAIFailures.Store(openAIUsageFlightKey(ctx, account.ID), result)
			} else {
				s.cache.openAIUsageCache.Store(account.ID, result)
				s.cache.openAIFailures.Delete(openAIUsageFlightKey(ctx, account.ID))
			}
		}
		return result
	}
	if s.cache != nil {
		// Restricted readers must never join a privileged model fallback.
		ch := s.cache.openAIUsageFlight.DoChan(openAIUsageFlightKey(ctx, account.ID), func() (any, error) {
			refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIUsageRefreshTimeout)
			defer cancel()
			return refresh(refreshCtx), nil
		})
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case shared := <-ch:
			if shared.Err != nil {
				return nil, shared.Err
			}
			result, ok := shared.Val.(*openAIUsageCacheEntry)
			if !ok || result == nil {
				return nil, fmt.Errorf("invalid OpenAI usage refresh result")
			}
			return result, nil
		}
	}
	return refresh(ctx), nil
}

func (s *AccountUsageService) queryOpenAIUsageSnapshot(ctx context.Context, account *Account) *openAIUsageCacheEntry {
	entry := &openAIUsageCacheEntry{timestamp: time.Now()}
	if s.openAIQuotaService == nil {
		entry.message, entry.errorCode = "OpenAI quota service is not configured", "quota_service_unavailable"
		return entry
	}
	// Prefer the read-only resource. A failed read may use one model request,
	// as explicitly enabled by the account usage refresh policy.
	quota, queryErr := s.openAIQuotaService.QueryUsage(withOpenAIQuotaUsageOnly(WithScopedAdminAccountOperation(ctx, "accounts.usage.read")), account.ID)
	updates := openAIQuotaDisplayUpdates(account, quota)
	if queryErr == nil && !hasOpenAIQuotaDisplayWindows(updates) {
		queryErr = infraerrors.New(http.StatusBadGateway, "OPENAI_QUOTA_WINDOWS_MISSING", "OpenAI quota query returned no usable windows")
	}
	if queryErr != nil && adminaccess.RestrictedFromContext(ctx) != nil {
		entry.message, entry.errorCode = "OpenAI quota query failed", "quota_query_failed"
		return entry
	}
	if queryErr != nil {
		var fallbackErr error
		updates, fallbackErr = s.openAIQuotaService.queryUsageFromModel(ctx, account)
		if fallbackErr != nil {
			// Keep authentication classification even when the other attempt
			// fails differently. Never expose upstream bodies or parse errors.
			classified := queryErr
			if code := infraerrors.Code(fallbackErr); code == http.StatusUnauthorized || code == http.StatusForbidden {
				classified = fallbackErr
			}
			entry.message, entry.errorCode = "OpenAI quota query and model fallback failed", "quota_query_failed"
			switch infraerrors.Code(classified) {
			case http.StatusUnauthorized:
				entry.message, entry.errorCode = "OpenAI quota query and model fallback failed: HTTP 401", errorCodeUnauthenticated
			case http.StatusForbidden:
				entry.message, entry.errorCode = "OpenAI quota query and model fallback failed: HTTP 403", errorCodeForbidden
			case http.StatusTooManyRequests:
				entry.message, entry.errorCode = "OpenAI quota query and model fallback failed: HTTP 429", errorCodeRateLimited
			}
			if infraerrors.Reason(classified) == "OPENAI_QUOTA_WINDOWS_MISSING" {
				entry.message, entry.errorCode = "OpenAI quota query and model fallback returned no usable windows", "quota_windows_missing"
			}
			if infraerrors.Reason(queryErr) == "OPENAI_QUOTA_NOT_CONFIGURED" {
				entry.message, entry.errorCode = "OpenAI quota service is not configured", "quota_service_unavailable"
			}
			return entry
		}
	}
	// Replace the displayed snapshot as a whole. Null absent windows so that an
	// old window is never presented under the new snapshot's acquisition time.
	for _, prefix := range []string{"codex_5h_", "codex_7d_", "codex_primary_", "codex_secondary_"} {
		for _, suffix := range []string{"used_percent", "reset_at", "reset_after_seconds", "window_minutes", "sampled_at"} {
			key := prefix + suffix
			if _, ok := updates[key]; !ok {
				updates[key] = nil
			}
		}
	}
	if s.accountRepo == nil {
		entry.message, entry.errorCode = "OpenAI quota snapshot storage is not configured", "quota_service_unavailable"
		return entry
	}
	if err := scopedAccountAccess(ctx, s.accountRepo, account, "accounts.usage.read", true); err != nil {
		entry.message, entry.errorCode = "Account access denied", "account_access_denied"
		return entry
	}
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, updates); err != nil {
		entry.message, entry.errorCode = "Failed to save OpenAI quota snapshot", "quota_snapshot_write_failed"
		return entry
	}
	entry.updates = updates
	return entry
}

func hasOpenAIQuotaDisplayWindows(updates map[string]any) bool {
	present := false
	for _, key := range []string{"codex_5h_used_percent", "codex_7d_used_percent"} {
		value := updates[key]
		if value == nil {
			continue
		}
		percent, ok := value.(float64)
		if !ok || math.IsNaN(percent) || math.IsInf(percent, 0) {
			return false
		}
		present = true
	}
	return present
}

func openAIQuotaDisplayUpdates(account *Account, quota *OpenAIQuotaUsage) map[string]any {
	if quota == nil {
		return nil
	}
	fetchedAt := time.Unix(quota.FetchedAt, 0).UTC()
	if account.IsShadow() {
		return buildCodexSparkWindowExtraUpdates(quota, fetchedAt)
	}
	return buildOpenAIAutoResetUsageUpdates(quota, fetchedAt)
}
