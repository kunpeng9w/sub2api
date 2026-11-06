package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SaveQuotaEstimateSnapshot preserves newer observations under concurrent reads.
// It merges only one display key and never enqueues a scheduler rebuild.
func (r *accountRepository) SaveQuotaEstimateSnapshot(ctx context.Context, id int64, window string, snapshot service.QuotaEstimateSnapshot) (bool, error) {
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.SaveQuotaEstimateSnapshot(ctx, id, window, snapshot)
	}
	if (window != "5h" && window != "7d") || snapshot.TotalCost <= 0 || math.IsNaN(snapshot.TotalCost) || math.IsInf(snapshot.TotalCost, 0) || snapshot.SampledAt.IsZero() {
		return false, fmt.Errorf("invalid quota estimate snapshot")
	}
	snapshot.SampledAtUnixNano = snapshot.SampledAt.UnixNano()
	key := service.QuotaEstimateSnapshotPrefix + window
	payload, err := json.Marshal(map[string]any{key: snapshot})
	if err != nil {
		return false, err
	}
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE accounts
SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb, updated_at = NOW()
WHERE id = $2 AND deleted_at IS NULL AND
CASE WHEN jsonb_typeof(extra->($3::text)->'sampled_at_unix_nano') = 'number'
THEN (extra->($3::text)->>'sampled_at_unix_nano')::numeric ELSE 0 END < $4::numeric`,
		string(payload), id, key, snapshot.SampledAtUnixNano)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 && dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshot(ctx, id)
	}
	return count > 0, nil
}
