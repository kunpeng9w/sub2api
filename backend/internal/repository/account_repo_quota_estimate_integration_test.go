//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQuotaEstimateSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := testEntTx(t).Client()
	account := mustCreateAccount(t, client, &service.Account{Name: "quota-history", Extra: map[string]any{"unrelated": "keep"}})
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	now := time.Now().UTC().Truncate(time.Microsecond)
	save := func(window string, total float64, sample time.Time) bool {
		changed, err := repo.SaveQuotaEstimateSnapshot(ctx, account.ID, window, service.QuotaEstimateSnapshot{TotalCost: total, SampledAt: sample})
		require.NoError(t, err)
		return changed
	}
	require.True(t, save("5h", 7.33, now))
	require.True(t, save("7d", 48.87, now))
	require.False(t, save("5h", 99, now.Add(-time.Hour)))
	require.False(t, save("5h", 99, now))
	loaded, err := client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "keep", loaded.Extra["unrelated"])
	raw, err := json.Marshal(loaded.Extra["quota_estimate_5h"])
	require.NoError(t, err)
	var snapshot service.QuotaEstimateSnapshot
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	require.Equal(t, 7.33, snapshot.TotalCost)
	require.True(t, now.Equal(snapshot.SampledAt))
	require.True(t, save("5h", 8, now.Add(time.Minute)))
	loaded, err = client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	raw, err = json.Marshal(loaded.Extra["quota_estimate_5h"])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	require.Equal(t, 8.0, snapshot.TotalCost)
}
