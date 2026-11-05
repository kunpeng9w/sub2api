//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorManualViewerProxyRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := testEntTx(t).Client()
	first := mustCreateUser(t, client, &service.User{})
	second := mustCreateUser(t, client, &service.User{})
	proxy := mustCreateProxy(t, client, &service.Proxy{Name: "fixture-proxy", Host: "proxy.invalid"})
	// This repository recognizes the transaction context for mutations and uses the
	// transaction client for reads; no upstream clients or runners are constructed.
	repo := NewChannelMonitorRepository(client, integrationDB)
	m := &service.ChannelMonitor{Name: "manual-private", Provider: "openai", Endpoint: "https://upstream.invalid", APIKey: "encrypted-fixture", PrimaryModel: "fixture-model", Enabled: true, IntervalSeconds: 60, Visibility: "selected", AllowedUserIDs: []int64{first.ID, second.ID}, ProxyID: &proxy.ID}
	require.NoError(t, repo.Create(ctx, m))
	loaded, err := repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.False(t, loaded.AutoCheckEnabled)
	require.Equal(t, "selected", loaded.Visibility)
	require.ElementsMatch(t, []int64{first.ID, second.ID}, loaded.AllowedUserIDs)
	require.Equal(t, proxy.ID, *loaded.ProxyID)
	// The admin edit dialog starts with a list row, not a GetByID response.
	listed, total, err := repo.List(ctx, service.ChannelMonitorListParams{Search: m.Name, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, listed, 1)
	require.ElementsMatch(t, []int64{first.ID, second.ID}, listed[0].AllowedUserIDs)
	listed[0].Name = "renamed-private"
	require.NoError(t, repo.Update(ctx, listed[0]))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed-private", loaded.Name)
	require.ElementsMatch(t, []int64{first.ID, second.ID}, loaded.AllowedUserIDs)
	loaded.AllowedUserIDs = []int64{second.ID}
	loaded.AutoCheckEnabled = true
	require.NoError(t, repo.Update(ctx, loaded))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.True(t, loaded.AutoCheckEnabled)
	require.Equal(t, []int64{second.ID}, loaded.AllowedUserIDs)
	require.NoError(t, client.Proxy.DeleteOneID(proxy.ID).Exec(ctx))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, proxy.ID, *loaded.ProxyID, "soft deletion retains the binding for fail-closed resolution")
	_, err = client.Proxy.Get(ctx, proxy.ID)
	require.Error(t, err, "a soft-deleted proxy cannot be resolved")
	require.NoError(t, client.Proxy.DeleteOneID(proxy.ID).Exec(mixins.SkipSoftDelete(ctx)))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.Nil(t, loaded.ProxyID, "physical deletion clears the FK without creating a direct route")
	require.NoError(t, client.User.DeleteOneID(second.ID).Exec(ctx))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.AllowedUserIDs)
	require.Equal(t, "selected", loaded.Visibility, "last viewer deletion must not publish the monitor")
}
