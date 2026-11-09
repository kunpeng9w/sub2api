package repository

import (
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateSnapshotConditionalWriteWithoutOutbox(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "repeated", true: "new"}[changed], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			repo := newAccountRepositoryWithSQL(client, db, nil)
			sample := time.Now().UTC()
			count := int64(0)
			if changed {
				count = 1
			}
			mock.ExpectExec(`(?s)UPDATE accounts.*COALESCE\(extra.*deleted_at IS NULL.*sampled_at_unix_nano.*< \$4::numeric`).WithArgs(sqlmock.AnyArg(), int64(200), "quota_estimate_5h", sample.UnixNano()).WillReturnResult(sqlmock.NewResult(0, count))
			written, err := repo.SaveQuotaEstimateSnapshot(context.Background(), 200, "5h", service.QuotaEstimateSnapshot{TotalCost: 7.33, SampledAt: sample})
			require.NoError(t, err)
			require.Equal(t, changed, written)
			require.NoError(t, mock.ExpectationsWereMet())
			require.False(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{"quota_estimate_5h": nil}))
		})
	}
}
