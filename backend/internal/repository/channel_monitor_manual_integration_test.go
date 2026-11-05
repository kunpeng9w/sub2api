//go:build integration

package repository

import (
	"context"
	"testing"

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
	require.Nil(t, loaded.ProxyID, "proxy deletion retains monitor without granting a direct route")
	require.NoError(t, client.User.DeleteOneID(second.ID).Exec(ctx))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.AllowedUserIDs)
	require.Equal(t, "selected", loaded.Visibility, "last viewer deletion must not publish the monitor")
}
