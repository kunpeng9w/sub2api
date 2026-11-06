//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type estimateHistoryRepo struct {
	AccountRepository
	extra  map[int64]map[string]any
	writes int
}

func (r *estimateHistoryRepo) SaveQuotaEstimateSnapshot(_ context.Context, id int64, window string, snapshot QuotaEstimateSnapshot) (bool, error) {
	if r.extra[id] == nil {
		r.extra[id] = map[string]any{}
	}
	account := &Account{Extra: r.extra[id]}
	if old := quotaEstimateHistory(account, window, time.Now()); old != nil && !snapshot.SampledAt.After(old.SampledAt) {
		return false, nil
	}
	r.extra[id][QuotaEstimateSnapshotPrefix+window] = snapshot
	r.writes++
	return true, nil
}
func TestQuotaEstimateHistoricalInactiveAndRatio(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().Add(-time.Minute)
			end5 := now.Add(time.Hour)
			end7 := now.Add(24 * time.Hour)
			repo := &estimateHistoryRepo{extra: map[int64]map[string]any{}}
			svc := &AccountUsageService{accountRepo: repo, usageLogRepo: &estimateWindowRepo{}}
			account := &Account{ID: 200, Platform: platform}
			usage := &UsageInfo{UpdatedAt: &now, FiveHour: &UsageProgress{Utilization: 40, ResetsAt: &end5}, SevenDay: &UsageProgress{Utilization: 8, ResetsAt: &end7}}
			first := svc.attachQuotaEstimates(ctx, account, usage)
			require.InDelta(t, 20, *first.FiveHour.Estimate.SevenDaySharePercent, 1e-9)
			require.Equal(t, 2, repo.writes)
			require.Empty(t, repo.extra[100], "shadow capacity must not be saved to parent")
			// Restart: history survives without any process-local cache or billing reader.
			restarted := &AccountUsageService{accountRepo: repo}
			account.Extra = repo.extra[account.ID]
			inactive := &UsageInfo{UpdatedAt: &now, FiveHour: &UsageProgress{WindowInactive: true, SampledAt: &now}, SevenDay: &UsageProgress{WindowInactive: true, SampledAt: &now}}
			predicted := restarted.attachQuotaEstimates(ctx, account, inactive)
			require.Equal(t, "historical", predicted.FiveHour.Estimate.Source)
			require.Equal(t, 30.0, *predicted.FiveHour.Estimate.TotalCost)
			require.Equal(t, 30.0, *predicted.FiveHour.Estimate.RemainingCost)
			require.True(t, now.Equal(*predicted.FiveHour.Estimate.SampledAt))
			require.InDelta(t, 20, *predicted.FiveHour.Estimate.SevenDaySharePercent, 1e-9)
			require.Nil(t, predicted.FiveHour.ResetsAt)
			require.Equal(t, 2, repo.writes)
			svc.attachQuotaEstimates(ctx, account, usage)
			require.Equal(t, 2, repo.writes, "identical observation must not save again")
		})
	}
}
func TestQuotaEstimateHistoryDoesNotInventFullRemaining(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	expired := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	account := &Account{ID: 1, Platform: PlatformOpenAI, Extra: map[string]any{QuotaEstimateSnapshotPrefix + "5h": QuotaEstimateSnapshot{TotalCost: 7.33, SampledAt: now}}}
	for _, tc := range []struct {
		name    string
		p       *UsageProgress
		failure string
	}{
		{"missing window", nil, ""},
		{"elapsed deadline", &UsageProgress{ResetsAt: &expired, SampledAt: &now}, ""},
		{"unknown sample", &UsageProgress{WindowInactive: true, sampleTimeMissing: true}, ""},
		{"nonzero unknown window", &UsageProgress{Utilization: 10, SampledAt: &now}, ""},
		{"query failed", &UsageProgress{WindowInactive: true, SampledAt: &now}, "upstream failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &AccountUsageService{}
			result := svc.attachQuotaEstimates(context.Background(), account, &UsageInfo{FiveHour: tc.p, Error: tc.failure})
			if result.FiveHour != nil {
				require.Nil(t, result.FiveHour.Estimate.TotalCost)
			}
		})
	}
	svc := &AccountUsageService{}
	result := svc.attachQuotaEstimates(context.Background(), account, &UsageInfo{FiveHour: &UsageProgress{ResetsAt: &future, SampledAt: &now}})
	require.Equal(t, 7.33, *result.FiveHour.Estimate.RemainingCost)
	require.Nil(t, result.FiveHour.Estimate.SevenDaySharePercent, "missing denominator must not invent a ratio")
}
func TestQuotaEstimateClaudeUnusedObservation(t *testing.T) {
	sample := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		body     string
		inactive bool
	}{
		{`{"five_hour":{"utilization":0,"resets_at":null}}`, true},
		{`{"five_hour":null}`, false}, {`{}`, false}, {`{"five_hour":{}}`, false},
		{`{"five_hour":{"utilization":null}}`, false},
	} {
		var raw ClaudeUsageResponse
		require.NoError(t, json.Unmarshal([]byte(tc.body), &raw))
		svc := &AccountUsageService{}
		usage := svc.buildUsageInfo(&raw, &sample)
		require.Equal(t, tc.inactive, usage.FiveHour.WindowInactive, tc.body)
	}
}
func TestQuotaEstimateHistoryNewerObservationReplaces(t *testing.T) {
	ctx := context.Background()
	sample := time.Now().Add(-time.Minute)
	old := sample.Add(-time.Hour)
	end := sample.Add(time.Hour)
	repo := &estimateHistoryRepo{extra: map[int64]map[string]any{1: {QuotaEstimateSnapshotPrefix + "5h": QuotaEstimateSnapshot{TotalCost: 9, SampledAt: old}}}}
	account := &Account{ID: 1, Platform: PlatformAnthropic, Extra: repo.extra[1]}
	svc := &AccountUsageService{accountRepo: repo, usageLogRepo: &estimateWindowRepo{}}
	result := svc.attachQuotaEstimates(ctx, account, &UsageInfo{UpdatedAt: &sample, FiveHour: &UsageProgress{Utilization: 40, ResetsAt: &end}})
	require.Equal(t, "current", result.FiveHour.Estimate.Source)
	require.Equal(t, 30.0, *result.FiveHour.Estimate.TotalCost)
	history := quotaEstimateHistory(account, "5h", time.Now())
	require.True(t, sample.Equal(history.SampledAt))
	_, err := repo.SaveQuotaEstimateSnapshot(ctx, 1, "5h", QuotaEstimateSnapshot{TotalCost: 99, SampledAt: old})
	require.NoError(t, err)
	require.Equal(t, 30.0, quotaEstimateHistory(account, "5h", time.Now()).TotalCost)
}
