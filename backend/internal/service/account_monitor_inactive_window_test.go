//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildCodexUsageProgressInactiveWindowRepeatedQueries(t *testing.T) {
	for _, minute := range []int{34, 45} {
		sampledAt := time.Date(2026, 11, 6, 8, minute, 0, 0, time.UTC)
		zero := 0.0
		reset5, reset7 := 18000, 604800
		updates := buildCodexUsageExtraUpdates(&OpenAICodexUsageSnapshot{
			PrimaryUsedPercent: &zero, PrimaryWindowMinutes: new(300), PrimaryResetAfterSeconds: &reset5,
			SecondaryUsedPercent: &zero, SecondaryWindowMinutes: new(10080), SecondaryResetAfterSeconds: &reset7,
		}, sampledAt)
		for _, window := range []string{"5h", "7d"} {
			// Legacy snapshots already contain a calculated deadline. The
			// display must suppress it even when read well after sampling.
			require.NotEmpty(t, updates["codex_"+window+"_reset_at"])
			progress := buildObservedCodexUsageProgress(updates, window, sampledAt.Add(10*time.Minute))
			require.NotNil(t, progress)
			require.True(t, progress.WindowInactive)
			require.Zero(t, progress.Utilization)
			require.Nil(t, progress.ResetsAt)
			require.Zero(t, progress.RemainingSeconds)
			require.Equal(t, sampledAt, *progress.SampledAt)
		}
	}
}

func TestBuildCodexUsageProgressInactiveWindowBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 6, 8, 45, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		used          float64
		resetAfter    int
		windowMinutes int
		inactive      bool
	}{
		{"legacy full window", 0, 18000, 0, true},
		{"custom full window", 0, 14400, 240, true},
		{"zero percent active window", 0, 17999, 300, false},
		{"fractional usage", 0.1, 18000, 300, false},
		{"nonzero usage", 12, 18000, 300, false},
		{"missing relative reset", 0, 0, 300, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAt := now.Add(time.Hour)
			progress := buildObservedCodexUsageProgress(map[string]any{
				"codex_5h_sample_complete":     true,
				"codex_5h_used_percent":        tc.used,
				"codex_5h_reset_after_seconds": tc.resetAfter,
				"codex_5h_window_minutes":      tc.windowMinutes,
				"codex_5h_reset_at":            resetAt.Format(time.RFC3339),
			}, "5h", now)
			require.NotNil(t, progress)
			require.Equal(t, tc.inactive, progress.WindowInactive)
			require.Equal(t, tc.used, progress.Utilization)
			if tc.inactive {
				require.Nil(t, progress.ResetsAt)
			} else {
				require.Equal(t, resetAt, *progress.ResetsAt)
				require.Equal(t, 3600, progress.RemainingSeconds)
			}
		})
	}
}

func TestUsageQuotaTiersPreservesInactiveWindow(t *testing.T) {
	tiers := usageQuotaTiers(&UsageInfo{
		FiveHour: &UsageProgress{WindowInactive: true},
		SevenDay: &UsageProgress{Utilization: 12},
	})
	require.Len(t, tiers, 2)
	require.True(t, tiers[0].WindowInactive)
	require.Empty(t, tiers[0].ResetAt)
	require.False(t, tiers[1].WindowInactive)
}

func TestBuildCodexUsageProgressPartialObservationInvalidatesInactive(t *testing.T) {
	for _, minutes := range []int{300, 10080} {
		window := "5h"
		if minutes == 10080 {
			window = "7d"
		}
		for _, field := range []string{"used", "reset", "minutes"} {
			t.Run(window+"/"+field, func(t *testing.T) {
				sample := time.Now().UTC().Add(-time.Minute)
				zero, reset := 0.0, minutes*60
				full := &OpenAICodexUsageSnapshot{PrimaryUsedPercent: &zero, PrimaryResetAfterSeconds: &reset, PrimaryWindowMinutes: &minutes}
				extra := buildCodexUsageExtraUpdates(full, sample)
				key := "codex_" + window
				other := "7d"
				if window == "7d" {
					other = "5h"
				}
				require.NotContains(t, extra, "codex_"+other+"_sample_complete")
				extra[QuotaEstimateSnapshotPrefix+window] = QuotaEstimateSnapshot{TotalCost: 30, SampledAt: sample.Add(-time.Hour)}
				require.True(t, buildObservedCodexUsageProgress(extra, window, sample).WindowInactive)
				partial := &OpenAICodexUsageSnapshot{PrimaryWindowMinutes: &minutes}
				switch field {
				case "used":
					partial.PrimaryUsedPercent = &zero
				case "reset":
					partial.PrimaryResetAfterSeconds = &reset
				}
				merge := func(snapshot *OpenAICodexUsageSnapshot, at time.Time) {
					for k, v := range buildCodexUsageExtraUpdates(snapshot, at) {
						extra[k] = v
					}
				}
				merge(partial, sample.Add(time.Second))
				if field == "used" {
					require.Nil(t, extra[key+"_reset_after_seconds"], "a new utilization without reset clears the old deadline")
				} else {
					require.Equal(t, reset, extra[key+"_reset_after_seconds"], "metadata-only updates retain the prior deadline")
				}
				p := buildObservedCodexUsageProgress(extra, window, time.Now())
				require.False(t, p.WindowInactive)
				require.Nil(t, p.ResetsAt)
				usage := &UsageInfo{}
				if window == "5h" {
					usage.FiveHour = p
				} else {
					usage.SevenDay = p
				}
				svc := &AccountUsageService{}
				result := svc.attachQuotaEstimates(context.Background(), &Account{ID: 1, Platform: PlatformOpenAI, Extra: extra}, usage)
				if window == "5h" {
					require.Nil(t, result.FiveHour.Estimate.TotalCost)
				} else {
					require.Nil(t, result.SevenDay.Estimate.TotalCost)
				}
				merge(full, sample.Add(2*time.Second))
				require.True(t, buildObservedCodexUsageProgress(extra, window, time.Now()).WindowInactive)
				reset--
				merge(full, sample.Add(3*time.Second))
				active := buildObservedCodexUsageProgress(extra, window, time.Now())
				require.False(t, active.WindowInactive)
				require.NotNil(t, active.ResetsAt)
			})
		}
	}
}

func TestBuildCodexUsageProgressLegacyObservationDoesNotProveInactive(t *testing.T) {
	now := time.Now().UTC()
	extra := map[string]any{
		"codex_5h_used_percent":        0.0,
		"codex_5h_reset_after_seconds": 18000,
		"codex_5h_sampled_at":          now.Format(time.RFC3339Nano),
	}
	require.False(t, buildObservedCodexUsageProgress(extra, "5h", now).WindowInactive)
}
