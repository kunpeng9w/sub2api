package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// EstimateQuota is pure: percentage is expressed as 0..100+, matching UsageProgress.
func EstimateQuota(cost, percentage float64, start, end, sampled, now time.Time) *domain.QuotaEstimate {
	e := &domain.QuotaEstimate{}
	if !sampled.IsZero() {
		t := sampled
		e.SampledAt = &t
	}
	switch {
	case start.IsZero() || end.IsZero() || sampled.IsZero() || !end.After(start) || sampled.Before(start) || !sampled.Before(end) || sampled.After(now) || !now.Before(end):
		e.UnavailableReason = "invalid_window"
	case math.IsNaN(cost) || math.IsInf(cost, 0) || math.IsNaN(percentage) || math.IsInf(percentage, 0) || cost <= 0 || percentage <= 0:
		e.UnavailableReason = "insufficient_usage"
	default:
		total := cost * 100 / percentage
		remaining := total * math.Max(0, 1-percentage/100)
		if math.IsInf(total, 0) || math.IsNaN(total) || math.IsInf(remaining, 0) || math.IsNaN(remaining) {
			e.UnavailableReason = "invalid_value"
			return e
		}
		e.Source = "current"
		e.TotalCost = &total
		e.RemainingCost = &remaining
	}
	return e
}

type UsageWindowQuery struct {
	Key       string    `json:"key"`
	AccountID int64     `json:"account_id"`
	Start     time.Time `json:"start_at"`
	End       time.Time `json:"end_at"`
	Family    string    `json:"family"`
}
type usageWindowBatchReader interface {
	GetUsageWindowStatsBatch(context.Context, []UsageWindowQuery) (map[string]*usagestats.AccountStats, error)
}

// attachQuotaEstimates queries only local billing data, ending at the quota sample time.
// Each model-family window has its own time range; a 5h cost is never reused for 7d.
func (s *AccountUsageService) attachQuotaEstimates(ctx context.Context, account *Account, usage *UsageInfo) *UsageInfo {
	if usage == nil || account == nil || (account.Platform != PlatformAnthropic && account.Platform != PlatformOpenAI) {
		return usage
	}
	copy := *usage
	usage = &copy
	now := time.Now()
	defer func() { s.finishQuotaEstimates(ctx, account, usage, now) }()
	sampled := time.Time{}
	if usage.UpdatedAt != nil {
		sampled = *usage.UpdatedAt
	}
	windows := []struct {
		p        **UsageProgress
		duration time.Duration
		family   string
	}{
		{&usage.FiveHour, 5 * time.Hour, ""}, {&usage.SevenDay, 7 * 24 * time.Hour, ""},
		{&usage.SevenDaySonnet, 7 * 24 * time.Hour, "sonnet"}, {&usage.SevenDayFable, 7 * 24 * time.Hour, "fable"},
	}
	queries := make([]UsageWindowQuery, 0, len(windows))
	progress := map[string]*UsageProgress{}
	for i, w := range windows {
		if *w.p == nil {
			continue
		}
		p := **w.p
		*w.p = &p
		windowSample := sampled
		if p.SampledAt != nil {
			windowSample = *p.SampledAt
		}
		if p.sampleTimeMissing {
			windowSample = time.Time{}
		}
		end := time.Time{}
		if p.ResetsAt != nil {
			end = *p.ResetsAt
		}
		start := end.Add(-w.duration)
		p.Estimate = EstimateQuota(0, p.Utilization, start, end, windowSample, now)
		// Validate the time range before touching local stats.
		if p.Estimate.UnavailableReason == "invalid_window" {
			continue
		}
		key := fmt.Sprintf("%d:%d:%d:%d:%s", account.ID, i, start.UnixNano(), windowSample.UnixNano(), w.family)
		queries = append(queries, UsageWindowQuery{Key: key, AccountID: account.ID, Start: start, End: windowSample, Family: w.family})
		progress[key] = &p
	}
	if len(queries) == 0 {
		return usage
	}
	reader, ok := s.usageLogRepo.(usageWindowBatchReader)
	if !ok {
		for _, p := range progress {
			p.Estimate.UnavailableReason = "stats_unavailable"
		}
		return usage
	}
	// Reuse the existing bounded-by-account cache; the key includes window and sample time.
	cacheKey := fmt.Sprintf("estimates:%d", account.ID)
	var stats map[string]*usagestats.AccountStats
	signature := fmt.Sprint(queries)
	if s.cache != nil {
		if cached, ok := s.cache.windowStatsCache.Load(cacheKey); ok {
			if c, ok := cached.(*quotaEstimateCache); ok && c.signature == signature && time.Since(c.at) < time.Minute {
				stats = c.stats
			}
		}
	}
	if stats == nil {
		var err error
		stats, err = reader.GetUsageWindowStatsBatch(ctx, queries)
		if err != nil {
			for _, p := range progress {
				p.Estimate.UnavailableReason = "stats_unavailable"
			}
			return usage
		}
		if s.cache != nil {
			s.cache.windowStatsCache.Store(cacheKey, &quotaEstimateCache{signature: signature, stats: stats, at: now})
		}
	}
	for _, q := range queries {
		p := progress[q.Key]
		st := stats[q.Key]
		if st == nil {
			p.Estimate.UnavailableReason = "stats_unavailable"
			continue
		}
		p.WindowStats = windowStatsFromAccountStats(st)
		p.Estimate = EstimateQuota(st.Cost, p.Utilization, q.Start, p.ResetsAt.UTC(), q.End, now)
	}
	return usage
}

type quotaEstimateCache struct {
	signature string
	stats     map[string]*usagestats.AccountStats
	at        time.Time
}

func stampUsageWindows(usage *UsageInfo, sampled *time.Time) {
	for _, p := range []*UsageProgress{usage.FiveHour, usage.SevenDay, usage.SevenDaySonnet, usage.SevenDayFable} {
		if p != nil {
			p.SampledAt = sampled
			p.sampleTimeMissing = sampled == nil
		}
	}
}
func setPassiveWindowSample(p *UsageProgress, extra map[string]any, window string) {
	setUsageWindowSample(p, extra["passive_usage_"+window+"_sampled_at"])
}

func setUsageWindowSample(p *UsageProgress, value any) {
	if p == nil {
		return
	}
	sample := time.Time{}
	if raw, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			sample = parsed
		}
	}
	// Unknown historical sample times must not be replaced by the latest page-read time.
	if sample.IsZero() {
		p.SampledAt = nil
		p.sampleTimeMissing = true
	} else {
		p.SampledAt = &sample
		p.sampleTimeMissing = false
	}
}
func setPassiveWindowSamples(usage *UsageInfo, extra map[string]any) {
	for _, w := range []struct {
		p   *UsageProgress
		key string
	}{{usage.FiveHour, "5h"}, {usage.SevenDay, "7d"}, {usage.SevenDaySonnet, "7d_sonnet"}, {usage.SevenDayFable, "7d_oi"}} {
		setPassiveWindowSample(w.p, extra, w.key)
	}
}

// QuotaEstimateSnapshot is a display-only capacity observation. It is scoped to
// the queried account (including Spark shadows), never the credential account.
type QuotaEstimateSnapshot struct {
	TotalCost         float64   `json:"total_cost"`
	SampledAt         time.Time `json:"sampled_at"`
	SampledAtUnixNano int64     `json:"sampled_at_unix_nano"`
}

const QuotaEstimateSnapshotPrefix = "quota_estimate_"

type quotaEstimateSnapshotWriter interface {
	SaveQuotaEstimateSnapshot(context.Context, int64, string, QuotaEstimateSnapshot) (bool, error)
}

func quotaEstimateHistory(account *Account, window string, now time.Time) *QuotaEstimateSnapshot {
	raw, err := json.Marshal(account.Extra[QuotaEstimateSnapshotPrefix+window])
	if err != nil {
		return nil
	}
	var snapshot QuotaEstimateSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.TotalCost <= 0 || math.IsNaN(snapshot.TotalCost) || math.IsInf(snapshot.TotalCost, 0) || snapshot.SampledAt.IsZero() || snapshot.SampledAt.After(now) {
		return nil
	}
	return &snapshot
}

func (s *AccountUsageService) finishQuotaEstimates(ctx context.Context, account *Account, usage *UsageInfo, now time.Time) {
	history := map[string]*QuotaEstimateSnapshot{}
	for _, window := range []struct {
		key      string
		progress *UsageProgress
		duration time.Duration
	}{{"5h", usage.FiveHour, 5 * time.Hour}, {"7d", usage.SevenDay, 7 * 24 * time.Hour}} {
		h := quotaEstimateHistory(account, window.key, now)
		history[window.key] = h
		p := window.progress
		if p == nil {
			continue
		}
		e := p.Estimate
		if e != nil && e.TotalCost != nil && e.RemainingCost != nil && e.SampledAt != nil && e.UnavailableReason == "" {
			snapshot := QuotaEstimateSnapshot{TotalCost: *e.TotalCost, SampledAt: *e.SampledAt, SampledAtUnixNano: e.SampledAt.UnixNano()}
			if h == nil || snapshot.SampledAt.After(h.SampledAt) {
				history[window.key] = &snapshot
				if writer, ok := s.accountRepo.(quotaEstimateSnapshotWriter); ok {
					if _, err := writer.SaveQuotaEstimateSnapshot(ctx, account.ID, window.key, snapshot); err != nil {
						slog.Warn("quota_estimate_snapshot_write_failed", "account_id", account.ID, "window", window.key)
					}
				}
			}
			continue
		}
		// Only an observed zero/unused window can regain full capacity. A
		// missing window or an elapsed deadline is not evidence of a reset.
		sample := p.SampledAt
		if sample == nil && !p.sampleTimeMissing {
			sample = usage.UpdatedAt
		}
		trustedZero := sample != nil && !sample.IsZero() && !sample.After(now) && !p.sampleTimeMissing && p.Utilization == 0 &&
			(p.WindowInactive || (p.ResetsAt != nil && now.Before(*p.ResetsAt) && sample.Before(*p.ResetsAt) && !sample.Before(p.ResetsAt.Add(-window.duration))))
		if h != nil && usage.Error == "" && usage.ErrorCode == "" && trustedZero {
			total := h.TotalCost
			p.Estimate = &domain.QuotaEstimate{Source: "historical", TotalCost: &total, RemainingCost: &total, SampledAt: &h.SampledAt}
		}
	}
	five := usage.FiveHour
	if five == nil || five.Estimate == nil || five.Estimate.TotalCost == nil || five.Estimate.UnavailableReason != "" {
		return
	}
	var sevenTotal float64
	if seven := usage.SevenDay; seven != nil && seven.Estimate != nil && seven.Estimate.TotalCost != nil {
		sevenTotal = *seven.Estimate.TotalCost
	} else if h := history["7d"]; h != nil {
		sevenTotal = h.TotalCost
	}
	if sevenTotal > 0 {
		share := *five.Estimate.TotalCost / sevenTotal * 100
		if !math.IsNaN(share) && !math.IsInf(share, 0) {
			five.Estimate.SevenDaySharePercent = &share
		}
	}
}
