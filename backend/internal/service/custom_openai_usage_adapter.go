package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const openAIUsageRefreshTimeout = time.Minute

type openAIUsageCacheEntry struct {
	timestamp time.Time
	cached    bool
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

// Refresh work is separated by permission scope; monitor work never enters it.
func openAIUsageFlightKey(ctx context.Context, accountID int64) string {
	scope := "full"
	if adminaccess.RestrictedFromContext(ctx) != nil {
		scope = "read-only"
	}
	return fmt.Sprintf("usage:%d:%s", accountID, scope)
}

func (s *AccountUsageService) cachedCodexRefresh(ctx context.Context, accountID int64) *openAIUsageCacheEntry {
	if s.cache == nil {
		return nil
	}
	value, _ := s.cache.openAIUsageCache.Load(openAIUsageFlightKey(ctx, accountID))
	entry, _ := value.(*openAIUsageCacheEntry)
	return entry
}

// The upstream ten-minute probe throttle applies to successful and failed
// attempts. Only in-flight cancellation and permission separation are local.
func (s *AccountUsageService) refreshOpenAICodexSnapshot(ctx context.Context, account *Account, force bool) (*openAIUsageCacheEntry, error) {
	refresh := func(refreshCtx context.Context) *openAIUsageCacheEntry {
		if cached := s.cachedCodexRefresh(ctx, account.ID); !force && cached != nil && time.Since(cached.timestamp) < openAIProbeCacheTTL {
			entry := *cached
			entry.cached = true
			return &entry
		}
		entry := &openAIUsageCacheEntry{timestamp: time.Now()}
		var err error
		if adminaccess.RestrictedFromContext(refreshCtx) != nil || account.IsShadow() {
			if s.openAIQuotaService == nil {
				err = fmt.Errorf("OpenAI quota service is not configured")
			} else {
				var quota *OpenAIQuotaUsage
				quota, err = s.openAIQuotaService.QueryUsage(withOpenAIQuotaUsageOnly(WithScopedAdminAccountOperation(refreshCtx, "accounts.usage.read")), account.ID)
				if err == nil {
					entry.updates = openAIQuotaDisplayUpdates(account, quota)
				}
			}
		} else {
			entry.updates, err = s.probeOpenAICodexSnapshot(refreshCtx, account)
		}
		if err == nil && !hasOpenAIQuotaDisplayWindows(entry.updates) {
			err = fmt.Errorf("quota windows missing")
		}
		if err == nil && s.accountRepo != nil {
			err = scopedAccountAccess(refreshCtx, s.accountRepo, account, "accounts.usage.read", true)
			if err == nil {
				err = s.accountRepo.UpdateExtra(refreshCtx, account.ID, entry.updates)
			}
			if err == nil && adminaccess.RestrictedFromContext(refreshCtx) == nil {
				notifyOpenAIAutoReset(account.ID)
				if account.IsShadow() && account.ParentAccountID != nil {
					notifyOpenAIAutoReset(*account.ParentAccountID)
				}
			}
		}
		if err != nil {
			entry.updates = nil
			entry.message, entry.errorCode = "Unable to refresh OpenAI quota; displayed values may be from an earlier observation", errorCodeQuotaRefreshFailed
			if infraerrors.Code(err) == http.StatusUnauthorized || infraerrors.Reason(err) == "OPENAI_CREDENTIAL_INVALID" {
				entry.errorCode = errorCodeUnauthenticated
			}
			if infraerrors.Reason(err) == "OPENAI_ACCOUNT_BANNED" {
				entry.errorCode = "account_banned"
			}
		}
		if s.cache != nil {
			s.cache.openAIUsageCache.Store(openAIUsageFlightKey(ctx, account.ID), entry)
		}
		return entry
	}
	if s.cache == nil {
		return refresh(ctx), nil
	}
	ch := s.cache.openAIUsageFlight.DoChan(openAIUsageFlightKey(ctx, account.ID), func() (any, error) {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIUsageRefreshTimeout)
		defer cancel()
		return refresh(refreshCtx), nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		entry, ok := result.Val.(*openAIUsageCacheEntry)
		if !ok || entry == nil {
			return nil, fmt.Errorf("invalid OpenAI usage refresh result")
		}
		return entry, nil
	}
}

// Extract before generic refresh failure, including credential failures that
// arrive alongside stale quota headers. A generic 403 is not credential loss.
func extractOpenAICodexProbeUpdates(resp *http.Response) (map[string]any, error) {
	if resp == nil {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, infraerrors.New(http.StatusUnauthorized, "OPENAI_CREDENTIAL_INVALID", "OpenAI authentication failed")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, infraerrors.New(http.StatusForbidden, "OPENAI_PERMISSION_DENIED", "OpenAI request denied")
	}
	if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
		return buildCodexUsageExtraUpdates(snapshot, time.Now()), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, infraerrors.New(resp.StatusCode, "OPENAI_PROBE_FAILED", "OpenAI quota probe failed")
	}
	return nil, nil
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

// Restrict 403 credential classification to machine-readable upstream error
// codes. Permission, region, proxy and policy denials remain ordinary errors.
func openAICredentialErrorReason(body []byte) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	for _, code := range []string{payload.Error.Code, payload.Error.Type} {
		switch code {
		case "invalid_api_key", "token_expired", "invalid_token":
			return "OPENAI_CREDENTIAL_INVALID"
		case "account_deactivated", "account_disabled":
			return "OPENAI_ACCOUNT_BANNED"
		}
	}
	return ""
}
