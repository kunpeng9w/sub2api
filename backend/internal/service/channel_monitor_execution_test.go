//go:build unit

package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/monitorrequest"
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

// Reject canceled persistence contexts, just as the real database driver does.
type monitorContextRepo struct {
	*quotaModeRepoStub
}

func (r *monitorContextRepo) InsertHistoryBatch(ctx context.Context, rows []*ChannelMonitorHistoryRow) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.quotaModeRepoStub.InsertHistoryBatch(ctx, rows)
}

func (r *monitorContextRepo) MarkChecked(ctx context.Context, id int64, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.quotaModeRepoStub.MarkChecked(ctx, id, at)
}

func TestMonitorCanceledRunHoldsLockAndSlotUntilQuotaCompletes(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{
		ID: 1, Enabled: true, Visibility: "all", Provider: MonitorProviderOpenAI,
		PrimaryModel: "quota", CheckMode: MonitorCheckModeQuota, AccountID: int64Ptr(9),
	}}
	one := newQuotaModeService(t, repo)
	one.repo = &monitorContextRepo{repo}
	one.runSlots = make(chan struct{}, 1)
	locks := &monitorFixtureLocks{}
	one.runLocks = locks
	two := NewChannelMonitorService(nil, nil)
	two.runLocks = locks // A separate instance shares only Redis, not singleflight.
	usage := &stubMonitorUsageSource{usage: &UsageInfo{}, block: make(chan struct{})}
	one.SetQuotaFetcher(newQuotaModeFetcher(map[int64]*Account{
		9: {ID: 9, Platform: MonitorProviderOpenAI},
	}, usage))
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(usage.block) }) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := one.RunManual(ctx, 1, 10, false)
		done <- err
	}()
	require.Eventually(t, func() bool { return usage.getCalls() == 1 }, time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-done:
		t.Fatalf("run returned while quota query was still active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	_, err := two.acquireRun(context.Background(), 1, 20)
	require.ErrorIs(t, err, ErrChannelMonitorBusy, "another instance must not overlap the active query")
	_, err = one.acquireRun(context.Background(), 2, 20)
	require.ErrorIs(t, err, ErrChannelMonitorBusy, "the local execution slot must remain occupied")
	unblock.Do(func() { close(usage.block) })
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("completed quota query did not release the run")
	}
	require.Len(t, repo.history, 1, "results must survive a client disconnect")
	require.Equal(t, []int64{1}, repo.markedIDs)
	require.Empty(t, one.runSlots)
	release, err := two.acquireRun(context.Background(), 1, 20)
	require.NoError(t, err)
	release()
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
func TestMonitorMissingAccountAndUnauthorizedNeverReachQuota(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, Visibility: "selected", AllowedUserIDs: []int64{10}, CheckMode: MonitorCheckModeQuota, Provider: MonitorProviderOpenAI, AccountID: int64Ptr(9)}}
	svc := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
	svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	_, err := svc.RunManual(context.Background(), 1, 20, false)
	require.ErrorIs(t, err, ErrChannelMonitorNotFound)
	_, err = svc.RunManual(context.Background(), 1, 10, false)
	require.ErrorIs(t, err, ErrChannelMonitorEgress)
	require.Empty(t, repo.history)
}

func TestMonitorRouteRevocationInvalidatesPinnedContext(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, AutoCheckEnabled: true, ProxyID: int64Ptr(71), Provider: MonitorProviderOpenAI}}
	proxies := &monitorFixtureProxyRepo{}
	svc := NewChannelMonitorService(repo, nil)
	svc.proxies = proxies
	ctx, err := svc.monitorContext(context.Background(), repo.monitor)
	require.NoError(t, err)
	policy, ok := monitorrequest.From(ctx)
	require.True(t, ok)
	require.NoError(t, monitorrequest.Check(ctx, policy.Route))
	repo.monitor.AutoCheckEnabled = false
	require.ErrorIs(t, monitorrequest.Check(ctx, policy.Route), ErrChannelMonitorDisabled)
	manual, err := svc.monitorContext(context.Background(), repo.monitor, 10)
	require.NoError(t, err)
	require.NoError(t, monitorrequest.Check(manual, policy.Route))
	repo.monitor.AutoCheckEnabled = true
	proxies.broken = true
	require.Error(t, monitorrequest.Check(ctx, policy.Route))
	proxies.broken = false
	repo.monitor.ProxyID = nil
	require.Error(t, monitorrequest.Check(ctx, policy.Route))
}

func TestMonitorDirectAccountQuotaAndRouteChanges(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{
		ID: 1, Enabled: true, Provider: MonitorProviderOpenAI,
		CheckMode: MonitorCheckModeQuota, AccountID: int64Ptr(9), PrimaryModel: "quota",
	}}
	svc := newQuotaModeService(t, repo)
	account := &Account{ID: 9, Platform: MonitorProviderOpenAI}
	usage := &stubMonitorUsageSource{usage: &UsageInfo{}}
	fetcher := newQuotaModeFetcher(map[int64]*Account{9: account}, usage)
	account.ProxyID = nil
	svc.SetQuotaFetcher(fetcher)
	svc.proxies = nil // Direct accounts do not require a proxy repository.

	results, err := svc.RunManual(context.Background(), 1, 10, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Quota.Success)
	require.Equal(t, 1, usage.getCalls())
	require.Len(t, repo.history, 1)

	direct, err := svc.monitorContext(context.Background(), repo.monitor, 10)
	require.NoError(t, err)
	require.NoError(t, monitorrequest.Check(direct, ""))
	proxies := &monitorFixtureProxyRepo{}
	svc.proxies = proxies
	account.ProxyID = int64Ptr(71)
	require.ErrorIs(t, monitorrequest.Check(direct, ""), ErrChannelMonitorEgress)
	proxied, err := svc.monitorContext(context.Background(), repo.monitor, 10)
	require.NoError(t, err)
	policy, _ := monitorrequest.From(proxied)
	require.Equal(t, "http://proxy.invalid:8080", policy.Route)
	require.NotEqual(t, monitorrequest.CacheKey(direct), monitorrequest.CacheKey(proxied))
	require.NoError(t, monitorrequest.Check(proxied, policy.Route))
	proxies.broken = true
	_, err = svc.monitorContext(context.Background(), repo.monitor, 10)
	require.ErrorIs(t, err, ErrChannelMonitorEgress)
	require.ErrorIs(t, monitorrequest.Check(proxied, policy.Route), ErrChannelMonitorEgress)
	account.ProxyID = nil
	require.ErrorIs(t, monitorrequest.Check(proxied, policy.Route), ErrChannelMonitorEgress)
	fresh, err := svc.monitorContext(context.Background(), repo.monitor, 10)
	require.NoError(t, err)
	require.NoError(t, monitorrequest.Check(fresh, ""))
}

func TestMonitorUnboundClaudeFollowsAccountDefault(t *testing.T) {
	account := &Account{ID: 9, Platform: MonitorProviderAnthropic, Type: AccountTypeOAuth}
	svc := NewChannelMonitorService(nil, nil)
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{ClaudeEgress: config.ClaudeEgressConfig{
		Enabled: true, ProxyURL: "http://127.0.0.1:7890",
	}}}
	svc.SetQuotaFetcher(&ChannelMonitorQuotaFetcher{accounts: &stubMonitorAccountSource{
		accounts: map[int64]*Account{9: account},
	}})
	monitor := &ChannelMonitor{Provider: MonitorProviderAnthropic, AccountID: int64Ptr(9)}
	route, _, err := svc.monitorRouteSnapshot(context.Background(), monitor)
	require.NoError(t, err)
	expected, err := ResolveAccountProxyURL(account, svc.cfg)
	require.NoError(t, err)
	require.Equal(t, expected, route)
	require.Equal(t, svc.cfg.Gateway.ClaudeEgress.ProxyURL, route)
}

// 各界面共用同一批历史；验证当前状态变化不会重写历史可用率。
type monitorStatusRepo struct {
	*quotaModeRepoStub
	latest []*ChannelMonitorLatest
}

func (r *monitorStatusRepo) ListEnabled(context.Context) ([]*ChannelMonitor, error) {
	m := *r.monitor
	return []*ChannelMonitor{&m}, nil
}
func (r *monitorStatusRepo) ListLatestForMonitorIDs(_ context.Context, ids []int64) (map[int64][]*ChannelMonitorLatest, error) {
	out := make(map[int64][]*ChannelMonitorLatest)
	for _, id := range ids {
		out[id] = r.latest
	}
	return out, nil
}
func (r *monitorStatusRepo) ComputeAvailabilityForMonitors(context.Context, []int64, int) (map[int64][]*ChannelMonitorAvailability, error) {
	return map[int64][]*ChannelMonitorAvailability{r.monitor.ID: {{Model: "main", AvailabilityPct: 87.5}}}, nil
}
func (r *monitorStatusRepo) ListRecentHistoryForMonitors(context.Context, []int64, map[int64]string, int) (map[int64][]*ChannelMonitorHistoryEntry, error) {
	return map[int64][]*ChannelMonitorHistoryEntry{r.monitor.ID: {{Model: "main", Status: MonitorStatusOperational}}}, nil
}
func (r *monitorStatusRepo) ListLatestPerModel(context.Context, int64) ([]*ChannelMonitorLatest, error) {
	return r.latest, nil
}
func (r *monitorStatusRepo) ComputeAvailability(context.Context, int64, int) ([]*ChannelMonitorAvailability, error) {
	return []*ChannelMonitorAvailability{{Model: "main", AvailabilityPct: 87.5}}, nil
}

func TestMonitorCurrentStatusAcrossViews(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	for _, tc := range []struct {
		name, want string
		mutate     func(*ChannelMonitor, *Account, *ChannelMonitorLatest, *ChannelMonitorService)
	}{
		{"healthy", MonitorStatusOperational, func(*ChannelMonitor, *Account, *ChannelMonitorLatest, *ChannelMonitorService) {}},
		{"monitor paused", MonitorStatusPaused, func(m *ChannelMonitor, _ *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			m.AutoCheckEnabled = false
		}},
		{"disabled", MonitorStatusPaused, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Status = StatusDisabled
		}},
		{"scheduling paused", MonitorStatusPaused, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Schedulable = false
		}},
		{"error beats paused scheduling", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Status, a.Schedulable = StatusError, false
		}},
		{"expired", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.AutoPauseOnExpired, a.ExpiresAt = true, &past
		}},
		{"automatically paused after expiry", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.AutoPauseOnExpired, a.ExpiresAt, a.Schedulable = true, &past, false
		}},
		{"deleted quota account", MonitorStatusUnknown, func(m *ChannelMonitor, _ *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			m.CheckMode, m.AccountID = MonitorCheckModeQuota, nil
		}},
		{"rate limited", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.RateLimitResetAt = &future
		}},
		{"overloaded", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.OverloadUntil = &future
		}},
		{"temporary block", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.TempUnschedulableUntil = &future
		}},
		{"model cooldown", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			setAccountModelRateLimitSnapshot(a, "main", future, "test", now)
		}},
		{"quota exhausted", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Extra = map[string]any{"quota_limit": 1.0, "quota_used": 1.0}
		}},
		{"daily exhausted", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Extra = map[string]any{"quota_daily_limit": 1.0, "quota_daily_used": 1.0, "quota_daily_start": now.Format(time.RFC3339)}
		}},
		{"weekly exhausted", MonitorStatusFailed, func(_ *ChannelMonitor, a *Account, _ *ChannelMonitorLatest, _ *ChannelMonitorService) {
			a.Extra = map[string]any{"quota_weekly_limit": 1.0, "quota_weekly_used": 1.0, "quota_weekly_start": now.Format(time.RFC3339)}
		}},
		{"quota query failed", MonitorStatusUnknown, func(_ *ChannelMonitor, _ *Account, l *ChannelMonitorLatest, _ *ChannelMonitorService) {
			l.Status = MonitorStatusError
		}},
		{"stale despite recent last_checked_at", MonitorStatusUnknown, func(m *ChannelMonitor, _ *Account, l *ChannelMonitorLatest, _ *ChannelMonitorService) {
			m.LastCheckedAt, l.CheckedAt = &now, past
		}},
		{"no history", MonitorStatusUnknown, func(_ *ChannelMonitor, _ *Account, l *ChannelMonitorLatest, _ *ChannelMonitorService) {
			l.CheckedAt = time.Time{}
		}},
		{"proxy broken", MonitorStatusUnknown, func(_ *ChannelMonitor, _ *Account, _ *ChannelMonitorLatest, svc *ChannelMonitorService) {
			svc.proxies = &monitorFixtureProxyRepo{broken: true}
		}},
		{"account read failed", MonitorStatusUnknown, func(_ *ChannelMonitor, _ *Account, _ *ChannelMonitorLatest, svc *ChannelMonitorService) {
			svc.quotaFetcher.accounts = &stubMonitorAccountSource{err: fmt.Errorf("database unavailable")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, Provider: MonitorProviderOpenAI, PrimaryModel: "main", ExtraModels: []string{"other"}, IntervalSeconds: 60, JitterSeconds: 10, AccountID: int64Ptr(9)}}
			svc := newQuotaModeService(t, repo)
			account := &Account{ID: 9, Platform: MonitorProviderOpenAI, Type: AccountTypeAPIKey}
			svc.SetQuotaFetcher(newQuotaModeFetcher(map[int64]*Account{9: account}, nil))
			latest := &ChannelMonitorLatest{Model: "main", Status: MonitorStatusOperational, CheckedAt: now}
			fixture := &monitorStatusRepo{quotaModeRepoStub: repo, latest: []*ChannelMonitorLatest{latest}}
			svc.repo = fixture
			tc.mutate(repo.monitor, account, latest, svc)
			summary := svc.BatchMonitorStatusSummary(context.Background(), []*ChannelMonitor{repo.monitor})[1]
			views, err := svc.ListUserView(context.Background())
			require.NoError(t, err)
			detail, err := svc.GetUserDetail(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, tc.want, summary.PrimaryStatus)
			require.Equal(t, tc.want, views[0].PrimaryStatus)
			require.Equal(t, tc.want, detail.Models[0].LatestStatus)
			require.Equal(t, 87.5, views[0].Availability7d)
			require.Equal(t, MonitorStatusOperational, views[0].Timeline[0].Status)
			if tc.name == "model cooldown" {
				extra := &ChannelMonitorLatest{Model: "other", Status: MonitorStatusOperational, CheckedAt: now}
				statusFor := svc.monitorDisplayStatus(context.Background(), repo.monitor, make(map[int64]*Account))
				require.Equal(t, MonitorStatusOperational, statusFor("other", extra))
			}
		})
	}
}

func TestMonitorStatusReusesAccountAndHonorsJitter(t *testing.T) {
	repo := &quotaModeRepoStub{monitor: &ChannelMonitor{ID: 1, Enabled: true, Provider: MonitorProviderOpenAI, PrimaryModel: "main", IntervalSeconds: 60, JitterSeconds: 30, AccountID: int64Ptr(9)}}
	svc := newQuotaModeService(t, repo)
	svc.SetQuotaFetcher(newQuotaModeFetcher(map[int64]*Account{9: {ID: 9, Platform: MonitorProviderOpenAI}}, nil))
	accountSource := svc.quotaFetcher.accounts.(*stubMonitorAccountSource)
	cache := make(map[int64]*Account)
	for range 2 {
		statusFor := svc.monitorDisplayStatus(context.Background(), repo.monitor, cache)
		require.Equal(t, MonitorStatusOperational, statusFor("main", &ChannelMonitorLatest{Status: MonitorStatusOperational, CheckedAt: time.Now().Add(-240 * time.Second)}))
		require.Equal(t, MonitorStatusUnknown, statusFor("main", &ChannelMonitorLatest{Status: MonitorStatusOperational, CheckedAt: time.Now().Add(-300 * time.Second)}))
	}
	require.Equal(t, 1, accountSource.calls)
}
