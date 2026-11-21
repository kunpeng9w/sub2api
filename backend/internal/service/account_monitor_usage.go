package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
)

// Monitor reads have their own route-scoped outer cache. They must not join UI
// singleflights that can lose the route or issue a model fallback/credit query.
func (s *AccountUsageService) getMonitorUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	switch {
	case account.IsAnthropicOAuthOrSetupToken():
		if account.Type == AccountTypeSetupToken || account.IsSyntheticUITest() {
			return s.getMonitorPassiveClaudeUsage(ctx, account)
		}
		raw, err := s.fetchOAuthUsageRaw(ctx, account)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		usage := s.buildMonitorClaudeUsage(raw, now)
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
		var quota monitorOpenAIQuotaResponse
		payload, err := s.openAIQuotaService.queryUsage(withOpenAIQuotaUsageOnly(ctx), account.ID, &quota)
		if err != nil {
			return nil, err
		}
		usage := buildMonitorOpenAIUsage(account, &quota, time.Unix(payload.FetchedAt, 0).UTC())
		if usage.FiveHour == nil && usage.SevenDay == nil {
			return nil, ErrChannelMonitorAccountNotSupportable
		}
		return usage, nil
	default:
		return s.getUsageForAccount(ctx, account, false)
	}
}

// GetMonitorUsageForAccount keeps quota monitoring independent of page refresh
// policy even for callers without an active transport policy (e.g. unit stubs).
func (s *AccountUsageService) GetMonitorUsageForAccount(ctx context.Context, account *Account) (*UsageInfo, error) {
	if account == nil {
		return nil, ErrAccountNotFound
	}
	if err := scopedAccountAccess(ctx, s.accountRepo, account, "accounts.usage.read", true); err != nil {
		return nil, err
	}
	usage, err := s.getMonitorUsage(ctx, account)
	return s.attachQuotaEstimates(ctx, account, usage), err
}

// UnmarshalJSON records the presence of the 5h observation for quota monitors.
// The account view continues to use the upstream numeric fields.
func (r *ClaudeUsageResponse) UnmarshalJSON(data []byte) error {
	type plain ClaudeUsageResponse
	var payload struct {
		*plain
		FiveHour *struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		} `json:"five_hour"`
	}
	*r = ClaudeUsageResponse{}
	payload.plain = (*plain)(r)
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	w := payload.FiveHour
	r.monitorFiveHourMissing = w == nil || w.Utilization == nil
	if w != nil {
		r.FiveHour.ResetsAt = w.ResetsAt
		if w.Utilization != nil {
			r.FiveHour.Utilization = *w.Utilization
			r.monitorFiveHourInactive = *w.Utilization == 0 && w.ResetsAt == ""
		}
	}
	return nil
}

func (s *AccountUsageService) buildMonitorClaudeUsage(raw *ClaudeUsageResponse, sampledAt time.Time) *UsageInfo {
	usage := s.buildUsageInfo(raw, &sampledAt)
	if usage.FiveHour != nil {
		if raw.monitorFiveHourMissing {
			usage.FiveHour = nil
		} else {
			usage.FiveHour.WindowInactive = raw.monitorFiveHourInactive
		}
	}
	stampUsageWindows(usage, &sampledAt)
	return usage
}

func (s *AccountUsageService) getMonitorPassiveClaudeUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	usage, err := s.getPassiveUsageForAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	usage.SevenDaySonnet = buildPassiveUsageWindow(account.Extra, "passive_usage_7d_sonnet_utilization", "passive_usage_7d_sonnet_reset")
	setPassiveWindowSamples(usage, account.Extra)
	// A newer gateway sample supersedes an old inactive-window observation.
	if p := usage.FiveHour; p != nil && p.SampledAt != nil {
		if inactive, _ := account.Extra["passive_usage_5h_window_inactive"].(bool); inactive {
			if stamp, err := parseTime(fmt.Sprint(account.Extra["passive_usage_5h_inactive_sampled_at"])); err == nil && stamp.Equal(*p.SampledAt) {
				p.WindowInactive = true
				p.Utilization = 0
				p.ResetsAt = nil
				p.RemainingSeconds = 0
			}
		}
	}
	return usage, nil
}

func buildObservedCodexUsageProgress(extra map[string]any, window string, now time.Time) *UsageProgress {
	if len(extra) == 0 {
		return nil
	}

	var (
		usedPercentKey string
		resetAfterKey  string
		resetAtKey     string
	)

	switch window {
	case "5h":
		usedPercentKey = "codex_5h_used_percent"
		resetAfterKey = "codex_5h_reset_after_seconds"
		resetAtKey = "codex_5h_reset_at"
	case "7d":
		usedPercentKey = "codex_7d_used_percent"
		resetAfterKey = "codex_7d_reset_after_seconds"
		resetAtKey = "codex_7d_reset_at"
	default:
		return nil
	}

	usedRaw, ok := extra[usedPercentKey]
	if !ok || usedRaw == nil {
		return nil
	}

	progress := &UsageProgress{Utilization: parseExtraFloat64(usedRaw)}
	// Partial gateway observations update the account timestamp even when this
	// window is absent. Only its own percentage sample can bound an estimate.
	setUsageWindowSample(progress, extra["codex_"+window+"_sampled_at"])
	complete, observed := extra["codex_"+window+"_sample_complete"].(bool)
	if observed && !complete && progress.Utilization == 0 {
		// A partial zero observation cannot prove inactivity or a full reset.
		// Keep nonzero windows on the existing sample-bounded estimate path.
		progress.sampleTimeMissing = true
		return progress
	}
	// An unused rolling window can report its full duration on every query.
	// Adding that duration to the sample time invents a moving reset deadline;
	// it is not evidence that the upstream window has started. Keep this check
	// against the sampled relative value, not a countdown aged against now.
	windowMinutes := parseExtraInt(extra["codex_"+window+"_window_minutes"])
	if windowMinutes <= 0 {
		if window == "5h" {
			windowMinutes = 5 * 60
		} else {
			windowMinutes = 7 * 24 * 60
		}
	}
	if complete && progress.Utilization == 0 && parseExtraInt(extra[resetAfterKey]) >= windowMinutes*60 {
		progress.WindowInactive = true
		return progress
	}
	if resetAtRaw, ok := extra[resetAtKey]; ok {
		if resetAt, err := parseTime(fmt.Sprint(resetAtRaw)); err == nil {
			progress.ResetsAt = &resetAt
			progress.RemainingSeconds = int(resetAt.Sub(now).Seconds())
			if progress.RemainingSeconds < 0 {
				progress.RemainingSeconds = 0
			}
		}
	}
	if progress.ResetsAt == nil {
		// Relative resets need a trusted acquisition time. Reading an old
		// snapshot must not start its reset countdown again.
		base, err := time.Time{}, fmt.Errorf("window sample time missing")
		if progress.SampledAt != nil {
			base, err = *progress.SampledAt, nil
		}
		if resetAfterSeconds := parseExtraInt(extra[resetAfterKey]); resetAfterSeconds > 0 && err == nil && !base.IsZero() {
			resetAt := base.Add(time.Duration(resetAfterSeconds) * time.Second)
			progress.ResetsAt = &resetAt
			progress.RemainingSeconds = max(0, int(resetAt.Sub(now).Seconds()))
		}
	}

	return progress
}

type monitorOpenAIQuotaResponse struct {
	RateLimit            *monitorOpenAIRateLimit `json:"rate_limit"`
	AdditionalRateLimits []struct {
		MeteredFeature string                  `json:"metered_feature"`
		RateLimit      *monitorOpenAIRateLimit `json:"rate_limit"`
	} `json:"additional_rate_limits"`
}

type monitorOpenAIRateLimit struct {
	PrimaryWindow   *monitorOpenAIRateLimitWindow `json:"primary_window"`
	SecondaryWindow *monitorOpenAIRateLimitWindow `json:"secondary_window"`
}

type monitorOpenAIRateLimitWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int      `json:"limit_window_seconds"`
	ResetAfterSeconds  *int     `json:"reset_after_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

func buildMonitorOpenAIUsage(account *Account, quota *monitorOpenAIQuotaResponse, sampledAt time.Time) *UsageInfo {
	limit := quota.RateLimit
	if account.IsShadow() {
		limit = nil
		for _, extra := range quota.AdditionalRateLimits {
			if extra.MeteredFeature == "codex_bengalfox" {
				limit = extra.RateLimit
				break
			}
		}
	}
	usage := &UsageInfo{UpdatedAt: &sampledAt, Source: "active"}
	if limit == nil {
		return usage
	}
	snapshot := &OpenAICodexUsageSnapshot{}
	for _, window := range []struct {
		raw     *monitorOpenAIRateLimitWindow
		used    **float64
		minutes **int
		reset   **int
	}{
		{limit.PrimaryWindow, &snapshot.PrimaryUsedPercent, &snapshot.PrimaryWindowMinutes, &snapshot.PrimaryResetAfterSeconds},
		{limit.SecondaryWindow, &snapshot.SecondaryUsedPercent, &snapshot.SecondaryWindowMinutes, &snapshot.SecondaryResetAfterSeconds},
	} {
		if window.raw == nil || window.raw.UsedPercent == nil {
			continue
		}
		*window.used = window.raw.UsedPercent
		minutes := window.raw.LimitWindowSeconds / 60
		*window.minutes = &minutes
		*window.reset = window.raw.ResetAfterSeconds
		if window.raw.ResetAt > 0 {
			seconds := max(0, int(window.raw.ResetAt-sampledAt.Unix()))
			*window.reset = &seconds
		}
	}
	updates := buildCodexUsageExtraUpdates(snapshot, sampledAt)
	now := time.Now()
	observed := monitorOpenAIUsageFromExtra(updates, now)
	usage.FiveHour, usage.SevenDay = observed.FiveHour, observed.SevenDay
	return usage
}

func monitorOpenAIUsageFromExtra(extra map[string]any, now time.Time) *UsageInfo {
	usage := &UsageInfo{
		FiveHour: buildObservedCodexUsageProgress(extra, "5h", now),
		SevenDay: buildObservedCodexUsageProgress(extra, "7d", now),
	}
	if usage.FiveHour != nil || usage.SevenDay != nil {
		if ts, err := parseTime(fmt.Sprint(extra["codex_usage_updated_at"])); err == nil && !ts.IsZero() {
			usage.UpdatedAt = &ts
		}
	}
	return usage
}

// Only upstream machine-readable credential errors classify a monitor's 403.
func monitorOpenAICredentialErrorReason(body []byte) string {
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
