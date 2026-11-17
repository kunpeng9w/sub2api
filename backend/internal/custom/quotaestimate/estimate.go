// Package quotaestimate contains the quota calculation without service dependencies.
package quotaestimate

import (
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
)

// EstimateQuota is pure: percentage is expressed as 0..100+, matching UsageProgress.
func Estimate(cost, percentage float64, start, end, sampled, now time.Time) *domain.QuotaEstimate {
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
