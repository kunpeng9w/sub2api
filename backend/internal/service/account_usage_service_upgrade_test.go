package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/stretchr/testify/require"
)

func upgradeUsageReader(allowed bool) context.Context {
	policy := adminaccess.Policy{}
	if allowed {
		policy.Rules = []adminaccess.Rule{{Permissions: []string{"accounts.usage.read"}, IncludeUngrouped: true, AccountTypes: []string{AccountTypeOAuth}, Platforms: []string{PlatformOpenAI}}}
	}
	return adminaccess.WithPrincipal(context.Background(), &adminaccess.Principal{KeyID: "upgrade-reader", Policy: policy})
}

func TestOpenAIUsageRefresh_UpgradeDeniedBeforeCacheOrFlight(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, false, false)
	_, err := svc.GetUsage(context.Background(), id, true)
	require.NoError(t, err)
	before := upstream.requestLog()
	ctx := upgradeUsageReader(false)
	account, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	_, err = svc.getOpenAIUsage(ctx, account, false)
	require.Error(t, err)
	_, err = svc.GetPassiveUsage(ctx, id)
	require.Error(t, err)
	_, err = svc.GetUsageForAccount(ctx, account, true)
	require.Error(t, err)
	_, _, err = svc.GetUsageBatch(ctx, []int64{id}, true)
	require.Error(t, err)
	require.Equal(t, before, upstream.requestLog())
}

func TestOpenAIUsageRefresh_UpgradeRestrictedReadNeverFallsBackOrResets(t *testing.T) {
	svc, _, upstream, id := quotaRefreshFixture(t, false, false)
	upstream.status = http.StatusBadGateway
	usage, err := svc.GetUsage(upgradeUsageReader(true), id, true)
	require.NoError(t, err)
	require.NotEmpty(t, usage.Error)
	require.Equal(t, []string{"GET /backend-api/wham/usage"}, upstream.requestLog())
	require.Empty(t, upstream.modelLog())
}

func TestOpenAIUsageRefresh_UpgradeRestrictedFlightAndCancellationIsolation(t *testing.T) {
	svc, _, upstream, id := quotaRefreshFixture(t, false, false)
	started := make(chan struct{}, 4)
	release := make(chan struct{}, 2)
	upstream.status = http.StatusBadGateway
	upstream.requestStarted, upstream.releaseRequest = started, release
	defer close(release)
	fullDone := make(chan error, 1)
	go func() { _, err := svc.GetUsage(context.Background(), id, true); fullDone <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("privileged refresh did not start")
	}
	ctx, cancel := context.WithCancel(upgradeUsageReader(true))
	defer cancel()
	readDone := make(chan error, 1)
	go func() { _, err := svc.GetUsage(ctx, id, true); readDone <- err }()
	select {
	case <-started: // A read-only refresh must not join the privileged flight.
	case <-time.After(2 * time.Second):
		t.Fatal("restricted reader joined privileged flight")
	}
	cancel()
	select {
	case err := <-readDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("caller cancellation blocked")
	}
	// Release both independent requests; only the privileged one may fall back.
	upstream.mu.Lock()
	upstream.releaseRequest = nil
	upstream.mu.Unlock()
	release <- struct{}{}
	release <- struct{}{}
	select {
	case err := <-fullDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("restricted caller cancelled shared upstream")
	}
	settled := svc.cache.openAIUsageFlight.DoChan(fmt.Sprintf("usage:%d:read-only", id), func() (any, error) { return nil, nil })
	select {
	case <-settled:
	case <-time.After(3 * time.Second):
		t.Fatal("read-only refresh did not finish")
	}
	require.Len(t, upstream.modelLog(), 1)
}

func TestOpenAIUsageRefresh_UpgradeFailureCacheIsolation(t *testing.T) {
	for _, restrictedFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("restricted_first=%t", restrictedFirst), func(t *testing.T) {
			svc, _, upstream, id := quotaRefreshFixture(t, false, false)
			upstream.status = http.StatusBadGateway
			full, reader := context.Background(), upgradeUsageReader(true)
			first, second := full, reader
			firstRequests, totalRequests := 2, 3
			if restrictedFirst {
				first, second = reader, full
				firstRequests = 1
			}
			failed, err := svc.GetUsage(first, id)
			require.NoError(t, err)
			require.NotEmpty(t, failed.Error)
			throttled, err := svc.GetUsage(first, id)
			require.NoError(t, err)
			require.Equal(t, failed.Error, throttled.Error)
			require.Len(t, upstream.requestLog(), firstRequests)
			passive, err := svc.GetPassiveUsage(second, id)
			require.NoError(t, err)
			require.Empty(t, passive.Error, "another policy's failure must not leak into passive reads")
			_, err = svc.GetUsage(second, id)
			require.NoError(t, err)
			require.Len(t, upstream.requestLog(), totalRequests, "another policy's failure must not throttle this refresh")
			require.Len(t, upstream.modelLog(), 1)
			_, err = svc.GetUsage(second, id)
			require.NoError(t, err)
			require.Len(t, upstream.requestLog(), totalRequests)
		})
	}
}

func TestOpenAIUsageRefresh_UpgradeRestrictedFailurePreservesSuccessfulFallback(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, false, false)
	upstream.status = http.StatusBadGateway
	upstream.modelStatus = http.StatusOK
	upstream.modelHeaders = fallbackQuotaHeaders()
	full, reader := context.Background(), upgradeUsageReader(true)
	// Keep an old account object to verify that the shared successful cache survives.
	account, err := repo.GetByID(full, id)
	require.NoError(t, err)
	failed, err := svc.GetUsage(reader, id)
	require.NoError(t, err)
	require.NotEmpty(t, failed.Error)
	fresh, err := svc.GetUsage(full, id)
	require.NoError(t, err)
	require.Empty(t, fresh.Error)
	require.Equal(t, 55.0, fresh.FiveHour.Utilization)
	require.Len(t, upstream.modelLog(), 1)
	shared, err := svc.GetUsage(reader, id)
	require.NoError(t, err)
	require.Empty(t, shared.Error, "a newer shared success supersedes an earlier failure")
	require.Len(t, upstream.requestLog(), 3)
	failed, err = svc.GetUsage(reader, id, true)
	require.NoError(t, err)
	require.NotEmpty(t, failed.Error)
	for _, passive := range []bool{false, true} {
		var usage *UsageInfo
		if passive {
			usage, err = svc.GetPassiveUsage(full, id)
		} else {
			usage, err = svc.GetUsageForAccount(full, account)
		}
		require.NoError(t, err)
		require.Empty(t, usage.Error)
		require.Equal(t, fresh.FiveHour.Utilization, usage.FiveHour.Utilization)
		require.Equal(t, fresh.UpdatedAt, usage.UpdatedAt)
	}
	require.Len(t, upstream.requestLog(), 4)
	require.Len(t, upstream.modelLog(), 1)
}
