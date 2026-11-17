//go:build unit

package quotaestimate

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateFormulaAndInvalidInputs(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)
	e := Estimate(12, 40, start, end, now, now)
	require.Equal(t, 30.0, *e.TotalCost)
	require.InDelta(t, 18.0, *e.RemainingCost, 1e-9)
	e = Estimate(12, 120, start, end, now, now)
	require.Equal(t, 0.0, *e.RemainingCost)
	for _, values := range [][2]float64{{0, 40}, {12, 0}, {12, -1}, {math.NaN(), 40}, {12, math.Inf(1)}} {
		e := Estimate(values[0], values[1], start, end, now, now)
		require.Nil(t, e.TotalCost)
		require.NotEmpty(t, e.UnavailableReason)
	}
	for _, sample := range []time.Time{{}, start.Add(-time.Second), now.Add(time.Second), end} {
		require.Nil(t, Estimate(12, 40, start, end, sample, now).TotalCost)
	}
	require.Nil(t, Estimate(12, 40, start, now, now.Add(-time.Second), now).TotalCost)
}
