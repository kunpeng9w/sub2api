package domain

import "time"

// QuotaEstimate contains display-only estimates, never full account billing stats.
type QuotaEstimate struct {
	TotalCost         *float64   `json:"total_cost,omitempty"`
	RemainingCost     *float64   `json:"remaining_cost,omitempty"`
	SampledAt         *time.Time `json:"sampled_at,omitempty"`
	UnavailableReason string     `json:"unavailable_reason,omitempty"`
}
