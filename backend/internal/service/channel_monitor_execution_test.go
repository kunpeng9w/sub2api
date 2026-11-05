//go:build unit

package service

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type monitorFixtureProxyRepo struct {
	ProxyRepository
	broken bool
}

func (r *monitorFixtureProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	if r.broken {
		return nil, fmt.Errorf("fixture failure")
	}
	return &Proxy{ID: 71, Protocol: "http", Host: "proxy.invalid", Port: 8080, Status: StatusActive}, nil
}

type monitorFixtureLocks struct {
	mu   sync.Mutex
	held map[string]string
	fail bool
}

func (l *monitorFixtureLocks) TryAcquireLeaderLock(_ context.Context, key, owner string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return false, fmt.Errorf("fixture redis failure")
	}
	if l.held == nil {
		l.held = map[string]string{}
	}
	if l.held[key] != "" {
		return false, nil
	}
	l.held[key] = owner
	return true, nil
}
func (l *monitorFixtureLocks) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] == owner {
		delete(l.held, key)
	}
	return nil
}
func TestMonitorExecutionLocksAndCooldown(t *testing.T) {
	locks := &monitorFixtureLocks{}
	one := NewChannelMonitorService(nil, nil)
	two := NewChannelMonitorService(nil, nil)
	one.runLocks = locks
	two.runLocks = locks
	release, err := one.acquireRun(context.Background(), 1, 10)
	require.NoError(t, err)
	_, err = two.acquireRun(context.Background(), 1, 20)
	require.ErrorIs(t, err, ErrChannelMonitorBusy)
	release()
	_, err = two.acquireRun(context.Background(), 1, 10)
	require.ErrorIs(t, err, ErrChannelMonitorCooldown)
	release, err = two.acquireRun(context.Background(), 1, 20)
	require.NoError(t, err)
	release()
	locks.fail = true
	_, err = one.acquireRun(context.Background(), 1, 30)
	require.ErrorIs(t, err, ErrChannelMonitorCoordination)
}
func TestMonitorVisibilityAndRevocation(t *testing.T) {
	m := &ChannelMonitor{ID: 1, Enabled: true, Visibility: "selected", AllowedUserIDs: []int64{10, 20}}
	require.True(t, monitorVisibleTo(m, 10))
	require.True(t, monitorVisibleTo(m, 20))
	require.False(t, monitorVisibleTo(m, 30))
	m.AllowedUserIDs = []int64{20}
	require.False(t, monitorVisibleTo(m, 10))
	m.Visibility = "admin_only"
	require.False(t, monitorVisibleTo(m, 20))
	m.Visibility = "all"
	require.True(t, monitorVisibleTo(m, 30))
	require.False(t, monitorVisibleTo(m, 0))
	m.Enabled = false
	require.False(t, monitorVisibleTo(m, 30))
}
func TestMonitorMissingProxyAndUnauthorizedNeverReachQuota(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, Visibility: "selected", AllowedUserIDs: []int64{10}, CheckMode: MonitorCheckModeQuota, Provider: MonitorProviderOpenAI}}
	svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	_, err := svc.RunManual(context.Background(), 1, 20, false)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
	_, err = svc.RunManual(context.Background(), 1, 10, false)
	require.ErrorIs(t, err, ErrChannelMonitorEgress)
	require.Empty(t, repo.history)
}

func TestMonitorRouteRevocationInvalidatesPinnedContext(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, ProxyID: int64Ptr(71), Provider: MonitorProviderOpenAI}}
	proxies := &monitorFixtureProxyRepo{}
	svc := NewChannelMonitorService(repo, nil)
	svc.proxies = proxies
	ctx, err := svc.monitorContext(context.Background(), repo.monitor)
	require.NoError(t, err)
	policy, ok := monitorrequest.From(ctx)
	require.True(t, ok)
	require.NoError(t, monitorrequest.Check(ctx, policy.Route))
	proxies.broken = true
	require.Error(t, monitorrequest.Check(ctx, policy.Route))
	proxies.broken = false
	repo.monitor.ProxyID = nil
	require.Error(t, monitorrequest.Check(ctx, policy.Route))
}
