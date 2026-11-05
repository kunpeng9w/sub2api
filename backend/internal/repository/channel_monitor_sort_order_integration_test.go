//go:build integration

package repository

import (
 "context"
 "testing"

 "github.com/Wei-Shaw/sub2api/internal/service"
 "github.com/stretchr/testify/require"
)

func TestChannelMonitorSortOrderRoundTrip(t *testing.T) {
 ctx := context.Background()
 client := testEntTx(t).Client()
 repo := NewChannelMonitorRepository(client, integrationDB)
 var monitors []*service.ChannelMonitor
 for _, order := range []int{20, 10, 10, 0} {
  m := &service.ChannelMonitor{Name: "sort-order-fixture", Provider: "openai", Endpoint: "https://upstream.invalid", APIKey: "fixture", PrimaryModel: "model", Enabled: true, IntervalSeconds: 60, SortOrder: order}
  require.NoError(t, repo.Create(ctx, m))
  loaded, err := repo.GetByID(ctx, m.ID)
  require.NoError(t, err)
  require.Equal(t, order, loaded.SortOrder)
  monitors = append(monitors, m)
 }
 expected := []int64{monitors[3].ID, monitors[1].ID, monitors[2].ID, monitors[0].ID}
 for i, id := range expected {
  rows, total, err := repo.List(ctx, service.ChannelMonitorListParams{Search: "sort-order-fixture", Page: i+1, PageSize: 1})
  require.NoError(t, err)
  require.EqualValues(t, 4, total)
  require.Len(t, rows, 1)
  require.Equal(t, id, rows[0].ID)
 }
 assertEnabled := func(want []int64) {
  t.Helper()
  rows, err := repo.ListEnabled(ctx)
  require.NoError(t, err)
  var got []int64
  for _, row := range rows { if row.Name == "sort-order-fixture" { got = append(got, row.ID) } }
  require.Equal(t, want, got)
 }
 assertEnabled(expected)
 require.NoError(t, repo.MarkChecked(ctx, monitors[0].ID, monitors[0].CreatedAt))
 assertEnabled(expected)
 monitors[1].Enabled = false
 require.NoError(t, repo.Update(ctx, monitors[1]))
 assertEnabled([]int64{expected[0], expected[2], expected[3]})
 monitors[1].Enabled = true
 require.NoError(t, repo.Update(ctx, monitors[1]))
 assertEnabled(expected)
 monitors[0].SortOrder = 0
 require.NoError(t, repo.Update(ctx, monitors[0]))
 assertEnabled([]int64{monitors[0].ID, monitors[3].ID, monitors[1].ID, monitors[2].ID})
}
