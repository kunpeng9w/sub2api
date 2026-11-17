//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)


type estimateWindowRepo struct {
	UsageLogRepository
	queries []UsageWindowQuery
	calls   int
}

func (r *estimateWindowRepo) GetUsageWindowStatsBatch(_ context.Context, q []UsageWindowQuery) (map[string]*usagestats.AccountStats, error) {
	r.calls++
	r.queries = q
	out := map[string]*usagestats.AccountStats{}
	for _, w := range q {
		out[w.Key] = &usagestats.AccountStats{Cost: 12}
	}
	return out, nil
}
func TestQuotaEstimateAllWindowsAndSampleCutoff(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			sample := time.Now().Add(-time.Minute)
			end5 := sample.Add(time.Hour)
			end7 := sample.Add(24 * time.Hour)
			repo := &estimateWindowRepo{}
			svc := &AccountUsageService{usageLogRepo: repo, cache: NewUsageCache()}
			account := &Account{ID: 12, Platform: platform}
			usage := &UsageInfo{UpdatedAt: &sample, FiveHour: &UsageProgress{Utilization: 40, ResetsAt: &end5}, SevenDay: &UsageProgress{Utilization: 40, ResetsAt: &end7}}
			if platform == PlatformAnthropic {
				usage.SevenDaySonnet = &UsageProgress{Utilization: 40, ResetsAt: &end7}
				usage.SevenDayFable = &UsageProgress{Utilization: 40, ResetsAt: &end7}
			}
			result := svc.attachQuotaEstimates(context.Background(), account, usage)
			require.Equal(t, 30.0, *result.FiveHour.Estimate.TotalCost)
			require.Nil(t, usage.FiveHour.Estimate, "caller snapshot must not mutate")
			want := 2
			if platform == PlatformAnthropic {
				want = 4
			}
			require.Len(t, repo.queries, want)
			for _, q := range repo.queries {
				require.Equal(t, sample, q.End)
				require.True(t, q.Start.Before(sample))
			}
			if platform == PlatformAnthropic {
				require.Equal(t, "sonnet", repo.queries[2].Family)
				require.Equal(t, "fable", repo.queries[3].Family)
			}
			svc.attachQuotaEstimates(context.Background(), account, usage)
			require.Equal(t, 1, repo.calls, "cached local stats reused")
		})
	}
}

func TestQuotaEstimateKeepsIndependentWindowSampleTimes(t *testing.T) {
	sample := time.Now().Add(-time.Minute)
	older := sample.Add(-time.Hour)
	reset := sample.Add(time.Hour)
	repo := &estimateWindowRepo{}
	svc := &AccountUsageService{usageLogRepo: repo}
	usage := &UsageInfo{UpdatedAt: &sample, FiveHour: &UsageProgress{Utilization: 40, ResetsAt: &reset, SampledAt: &sample}, SevenDayFable: &UsageProgress{Utilization: 40, ResetsAt: &reset, SampledAt: &older}}
	result := svc.attachQuotaEstimates(context.Background(), &Account{ID: 1, Platform: PlatformAnthropic}, usage)
	require.Equal(t, sample, repo.queries[0].End)
	require.Equal(t, older, repo.queries[1].End)
	require.Equal(t, older, *result.SevenDayFable.Estimate.SampledAt)
	usage.SevenDayFable.sampleTimeMissing = true
	usage.SevenDayFable.SampledAt = nil
	result = svc.attachQuotaEstimates(context.Background(), &Account{ID: 1, Platform: PlatformAnthropic}, usage)
	require.Nil(t, result.SevenDayFable.Estimate.TotalCost, "missing historical sample time must not become page-load time")
}
