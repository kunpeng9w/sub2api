//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateOpenAIPartialObservationKeepsOtherWindowCutoff(t *testing.T) {
	for _, updatedWindow := range []string{"5h", "7d"} {
		t.Run(updatedWindow, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			older := now.Add(-time.Hour)
			newer := now.Add(-time.Minute)
			used := 20.0
			minutes5, minutes7 := 300, 10080
			reset5, reset7 := 4*60*60, 6*24*60*60
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			mergeAccountExtra(account, buildCodexUsageExtraUpdates(&OpenAICodexUsageSnapshot{
				PrimaryUsedPercent:         &used,
				PrimaryWindowMinutes:       &minutes5,
				PrimaryResetAfterSeconds:   &reset5,
				SecondaryUsedPercent:       &used,
				SecondaryWindowMinutes:     &minutes7,
				SecondaryResetAfterSeconds: &reset7,
				UpdatedAt:                  older.Format(time.RFC3339),
			}, now))
			used = 40
			partial := &OpenAICodexUsageSnapshot{
				PrimaryWindowMinutes:   &minutes5,
				SecondaryWindowMinutes: &minutes7,
				UpdatedAt:              newer.Format(time.RFC3339),
			}
			if updatedWindow == "5h" {
				partial.PrimaryUsedPercent = &used
			} else {
				partial.SecondaryUsedPercent = &used
			}
			mergeAccountExtra(account, buildCodexUsageExtraUpdates(partial, now))
			repo := &estimateWindowRepo{}
			svc := &AccountUsageService{usageLogRepo: repo}
			usage := svc.attachQuotaEstimates(context.Background(), account, openAIUsageFromExtra(account.Extra, now))
			require.Equal(t, newer, *usage.UpdatedAt)
			require.Len(t, repo.queries, 2)
			for i, window := range []string{"5h", "7d"} {
				progress := usage.FiveHour
				if window == "7d" {
					progress = usage.SevenDay
				}
				wantSample, wantTotal := older, 60.0
				if window == updatedWindow {
					wantSample, wantTotal = newer, 30.0
				}
				require.Equal(t, wantSample, repo.queries[i].End, "billing must stop at this window's percentage sample")
				require.Equal(t, wantSample, *progress.SampledAt)
				require.Equal(t, wantSample, *progress.Estimate.SampledAt)
				require.Equal(t, wantTotal, *progress.Estimate.TotalCost)
			}
		})
	}
}

func TestQuotaEstimateOpenAIUnknownWindowSampleDoesNotUseAccountTimestamp(t *testing.T) {
	for _, sample := range []any{nil, "", "invalid", 123} {
		t.Run("sample="+fmt.Sprint(sample), func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			account := &Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{
				"codex_5h_used_percent":  20.0,
				"codex_7d_used_percent":  30.0,
				"codex_5h_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
				"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
				"codex_5h_sampled_at":    sample,
				"codex_7d_sampled_at":    sample,
				"codex_usage_updated_at": now.Format(time.RFC3339),
			}}
			repo := &estimateWindowRepo{}
			svc := &AccountUsageService{usageLogRepo: repo}
			usage := svc.attachQuotaEstimates(context.Background(), account, openAIUsageFromExtra(account.Extra, now))
			require.Equal(t, 20.0, usage.FiveHour.Utilization)
			require.Equal(t, 30.0, usage.SevenDay.Utilization)
			for _, progress := range []*UsageProgress{usage.FiveHour, usage.SevenDay} {
				require.Nil(t, progress.SampledAt)
				require.Nil(t, progress.Estimate.TotalCost)
				require.Equal(t, "invalid_window", progress.Estimate.UnavailableReason)
			}
			require.Zero(t, repo.calls)
		})
	}
}

func TestQuotaEstimateOpenAIMetadataDoesNotResamplePercentage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	older := now.Add(-time.Hour)
	minutes5, minutes7 := 300, 10080
	ratio := 50.0
	updates := buildCodexUsageExtraUpdates(&OpenAICodexUsageSnapshot{
		PrimaryWindowMinutes:        &minutes5,
		SecondaryWindowMinutes:      &minutes7,
		PrimaryOverSecondaryPercent: &ratio,
		UpdatedAt:                   now.Format(time.RFC3339),
	}, now)
	require.NotContains(t, updates, "codex_5h_sampled_at")
	require.NotContains(t, updates, "codex_7d_sampled_at")
	account := &Account{Extra: map[string]any{
		"codex_5h_used_percent":        20.0,
		"codex_5h_sampled_at":          older.Format(time.RFC3339),
		"codex_5h_reset_after_seconds": 7200,
	}}
	mergeAccountExtra(account, updates)
	usage := openAIUsageFromExtra(account.Extra, now)
	require.Equal(t, older, *usage.FiveHour.SampledAt)
	require.Equal(t, older.Add(2*time.Hour), *usage.FiveHour.ResetsAt, "another observation must not extend a relative reset")
}

func TestQuotaEstimateOpenAIActiveAndSparkQueriesStampOnlyReturnedWindow(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		for _, minutes := range []int{300, 10080} {
			now := time.Now().UTC().Truncate(time.Second)
			account := &Account{ID: 1, Platform: PlatformOpenAI}
			limit := &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{
				UsedPercent: 40, LimitWindowSeconds: int64(minutes * 60), ResetAfterSeconds: 3600,
			}}
			quota := &OpenAIQuotaUsage{RateLimit: limit, FetchedAt: now.Unix()}
			if shadow {
				parent := int64(2)
				account.ParentAccountID = &parent
				account.QuotaDimension = QuotaDimensionSpark
				quota.AdditionalRateLimits = []OpenAIAdditionalRateLimit{{MeteredFeature: "codex_bengalfox", RateLimit: limit}}
				quota.RateLimit = nil
			}
			updates := openAIQuotaDisplayUpdates(account, quota)
			usage := openAIUsageFromExtra(updates, now)
			progress, absent := usage.FiveHour, usage.SevenDay
			if minutes == 10080 {
				progress, absent = usage.SevenDay, usage.FiveHour
			}
			require.NotNil(t, progress)
			require.Nil(t, absent)
			require.Equal(t, now, *progress.SampledAt)
			repo := &estimateWindowRepo{}
			svc := &AccountUsageService{usageLogRepo: repo}
			svc.attachQuotaEstimates(context.Background(), account, usage)
			require.Len(t, repo.queries, 1)
			require.Equal(t, now, repo.queries[0].End)
		}
	}
}
