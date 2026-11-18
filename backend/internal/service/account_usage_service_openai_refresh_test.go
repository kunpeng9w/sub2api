package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

// Only the snapshot write is implemented. Any accidental rate-limit mutation,
// credit consumption or account-status update fails the test immediately.
type quotaRefreshRepo struct {
	AccountRepository
	mu       sync.Mutex
	accounts map[int64]*Account
	writes   []int64
	writeErr error
}

func cloneQuotaAccount(account *Account) *Account {
	copy := *account
	copy.Extra = make(map[string]any, len(account.Extra))
	for key, value := range account.Extra {
		copy.Extra[key] = value
	}
	return &copy
}

func (r *quotaRefreshRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if account := r.accounts[id]; account != nil {
		return cloneQuotaAccount(account), nil
	}
	return nil, fmt.Errorf("account not found")
}

func (r *quotaRefreshRepo) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	var accounts []*Account
	for _, id := range ids {
		account, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func (r *quotaRefreshRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.writeErr != nil {
		return r.writeErr
	}
	r.writes = append(r.writes, id)
	mergeAccountExtra(r.accounts[id], updates)
	return nil
}

type quotaRefreshUpstream struct {
	mu                sync.Mutex
	status            int
	body              string
	timeout           bool
	requests          []string
	credentials       []string
	modelStatus       int
	modelHeaders      http.Header
	models            []string
	recoverAfterModel bool
	requestStarted    chan struct{}
	releaseRequest    <-chan struct{}
}

func (u *quotaRefreshUpstream) requestLog() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.requests...)
}

func (u *quotaRefreshUpstream) modelLog() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.models...)
}

func (u *quotaRefreshUpstream) credentialLog() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.credentials...)
}

func (u *quotaRefreshUpstream) serve(w http.ResponseWriter, r *http.Request) {
	var model string
	if r.Method == http.MethodPost {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		model = payload.Model
	}
	u.mu.Lock()
	u.requests = append(u.requests, r.Method+" "+r.URL.Path)
	u.credentials = append(u.credentials, r.Header.Get("Authorization")+"|"+r.Header.Get("chatgpt-account-id"))
	status, body, timeout := u.status, u.body, u.timeout
	started, release := u.requestStarted, u.releaseRequest
	modelHeaders := u.modelHeaders.Clone()
	if r.Method == http.MethodPost {
		u.models = append(u.models, model)
		status, body = u.modelStatus, u.body
		if u.recoverAfterModel {
			u.status = http.StatusOK
		}
	}
	u.mu.Unlock()
	if release != nil {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
	}
	if timeout {
		select {
		case <-r.Context().Done():
		case <-time.After(150 * time.Millisecond):
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		for key, values := range modelHeaders {
			w.Header()[key] = values
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func quotaRefreshFixture(t *testing.T, shadow, oldSnapshot bool) (*AccountUsageService, *quotaRefreshRepo, *quotaRefreshUpstream, int64) {
	t.Helper()
	parent := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"chatgpt_account_id": "parent-org"}, Extra: map[string]any{}}
	childID := int64(200)
	child := &Account{ID: childID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark, Extra: map[string]any{}}
	accountID := parent.ID
	if shadow {
		accountID = childID
	}
	repo := &quotaRefreshRepo{accounts: map[int64]*Account{100: parent, 200: child}}
	if oldSnapshot {
		// Expired windows must retain the observed percentage after a query failure.
		repo.accounts[accountID].Extra = map[string]any{
			"codex_5h_used_percent": 37.0, "codex_7d_used_percent": 63.0,
			"codex_5h_reset_at": "2020-01-01T00:00:00Z", "codex_7d_reset_at": "2020-01-01T00:00:00Z",
			"codex_usage_updated_at":                       "2020-01-01T00:00:00Z",
			"openai_oauth_responses_websockets_v2_enabled": true,
		}
	}
	window := func(used float64, seconds int64) *OpenAIRateLimitWindow {
		return &OpenAIRateLimitWindow{UsedPercent: used, LimitWindowSeconds: seconds, ResetAfterSeconds: 3600}
	}
	payload := &OpenAIQuotaUsage{
		// Reversed primary/secondary order exercises canonical normalization.
		RateLimit: &OpenAIRateLimit{PrimaryWindow: window(24, 604800), SecondaryWindow: window(12, 18000)},
		AdditionalRateLimits: []OpenAIAdditionalRateLimit{{MeteredFeature: "codex_bengalfox",
			RateLimit: &OpenAIRateLimit{PrimaryWindow: window(8, 18000), SecondaryWindow: window(16, 604800)}}},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	upstream := &quotaRefreshUpstream{status: 200, body: string(body), modelStatus: 200, modelHeaders: fallbackQuotaHeaders()}
	server := httptest.NewServer(http.HandlerFunc(upstream.serve))
	t.Cleanup(server.Close)
	factory := newQuotaRedirectingFactory(server)
	// A short local client timeout makes timeout coverage deterministic without
	// waiting for the production 20-second deadline.
	quota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo,
		&stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(parent): "sensitive-quota-token"}}, nil), factory, nil)
	originalFactory := quota.privacyClientFactory
	quota.privacyClientFactory = func(proxy string) (client *req.Client, err error) {
		client, err = originalFactory(proxy)
		if err == nil {
			upstream.mu.Lock()
			timeout := upstream.timeout
			upstream.mu.Unlock()
			if timeout {
				client.SetTimeout(60 * time.Millisecond)
			} else {
				client.SetTimeout(5 * time.Second)
			}
		}
		return
	}
	return &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), openAIQuotaService: quota,
		usageLogRepo: &usageBatchLogRepoStub{}}, repo, upstream, accountID
}

func callQuotaRefresh(t *testing.T, svc *AccountUsageService, repo *quotaRefreshRepo, id int64, path string, force bool) (*UsageInfo, *domain.MonitorQuotaSnapshot) {
	t.Helper()
	ctx := context.Background()
	switch path {
	case "batch":
		usage, failures, err := svc.GetUsageBatch(ctx, []int64{id}, force)
		require.NoError(t, err)
		require.Empty(t, failures)
		require.NotNil(t, usage[id])
		return usage[id], nil
	case "monitor":
		fetcher := NewChannelMonitorQuotaFetcher(svc, nil, nil, repo, nil)
		return nil, fetcher.Fetch(ctx, id)
	default:
		usage, err := svc.GetUsage(ctx, id, force)
		require.NoError(t, err)
		require.NotNil(t, usage)
		return usage, nil
	}
}

func fallbackQuotaHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("x-codex-primary-used-percent", "55")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-primary-reset-after-seconds", "3600")
	headers.Set("x-codex-secondary-used-percent", "65")
	headers.Set("x-codex-secondary-window-minutes", "10080")
	headers.Set("x-codex-secondary-reset-after-seconds", "86400")
	return headers
}

// Signal when the caller reaches its wait select, so cancellation coverage does
// not depend on sleeps or goroutine scheduling.
type quotaRefreshWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *quotaRefreshWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestOpenAIUsageRefresh_CallerCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	for _, tc := range []struct{ cancelLeader, restricted bool }{{true, false}, {false, false}, {true, true}, {false, true}} {
		t.Run(fmt.Sprintf("cancelLeader=%t/restricted=%t", tc.cancelLeader, tc.restricted), func(t *testing.T) {
			ctx := context.Background()
			if tc.restricted {
				ctx = upgradeUsageReader(true)
			}
			svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			upstream.requestStarted, upstream.releaseRequest = started, release
			type result struct {
				usage *UsageInfo
				err   error
			}
			waitFor := func(ch <-chan struct{}) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
					t.Fatal("timed out waiting for refresh synchronization")
				}
			}
			awaitResult := func(ch <-chan result) result {
				t.Helper()
				select {
				case got := <-ch:
					return got
				case <-time.After(2 * time.Second):
					t.Fatal("caller did not finish promptly")
					return result{}
				}
			}
			startCaller := func() (context.CancelFunc, <-chan result) {
				ctx, cancel := context.WithCancel(ctx)
				t.Cleanup(cancel)
				tracked := &quotaRefreshWaitContext{Context: ctx, waiting: make(chan struct{})}
				results := make(chan result, 1)
				go func() {
					usage, err := svc.GetUsage(tracked, id)
					results <- result{usage: usage, err: err}
				}()
				waitFor(tracked.waiting)
				return cancel, results
			}
			cancelFirst, first := startCaller()
			waitFor(started)
			cancelSecond, second := startCaller()
			canceled, survivor := first, second
			if tc.cancelLeader {
				cancelFirst()
			} else {
				cancelSecond()
				canceled, survivor = second, first
			}
			require.ErrorIs(t, awaitResult(canceled).err, context.Canceled)
			require.Nil(t, svc.cachedCodexRefresh(ctx, id), "caller cancellation must not populate the cache")
			unblock()
			got := awaitResult(survivor)
			require.NoError(t, got.err)
			require.NotNil(t, got.usage)
			require.Empty(t, got.usage.Error)
			wantPercent := 55.0
			if tc.restricted {
				wantPercent = 12
			}
			require.Equal(t, wantPercent, got.usage.FiveHour.Utilization)
			again, err := svc.GetUsage(ctx, id)
			require.NoError(t, err)
			require.Empty(t, again.Error)
			require.Equal(t, got.usage.UpdatedAt, again.UpdatedAt)
			wantRequest := "POST /backend-api/codex/responses"
			if tc.restricted {
				wantRequest = "GET /backend-api/wham/usage"
			}
			require.Equal(t, []string{wantRequest}, upstream.requestLog())
			for _, credentials := range upstream.credentialLog() {
				require.Equal(t, "Bearer sensitive-quota-token|parent-org", credentials)
			}
			repo.mu.Lock()
			writes := append([]int64(nil), repo.writes...)
			repo.mu.Unlock()
			require.Equal(t, []int64{id}, writes)
		})
	}
}

func TestOpenAIUsageRefresh_AlreadyCanceledCallerDoesNotStartRefresh(t *testing.T) {
	svc, _, upstream, id := quotaRefreshFixture(t, false, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.GetUsage(ctx, id, true)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, svc.cachedCodexRefresh(ctx, id))
	require.Empty(t, upstream.requestLog())
}

func TestOpenAIUsageRefresh_AllEntryPoints(t *testing.T) {
	for _, path := range []string{"single", "batch", "monitor", "restricted"} {
		for _, shadow := range []bool{false, true} {
			for _, history := range []bool{false, true} {
				for _, scenario := range []string{"success", "401", "403", "token403", "banned", "429", "500", "timeout", "parse", "empty"} {
					t.Run(fmt.Sprintf("%s/shadow=%t/history=%t/%s", path, shadow, history, scenario), func(t *testing.T) {
						svc, repo, upstream, id := quotaRefreshFixture(t, shadow, history)
						status := 200
						switch scenario {
						case "401":
							status = 401
						case "403", "token403", "banned":
							status = 403
						case "429":
							status = 429
						case "500":
							status = 500
						case "timeout":
							upstream.timeout = true
						case "parse":
							upstream.body = `{"rate_limit": secret`
						case "empty":
							upstream.body = `{}`
						}
						upstream.status, upstream.modelStatus = status, status
						if scenario != "success" {
							upstream.modelHeaders = nil
						}
						if scenario == "token403" {
							upstream.body = `{"error":{"code":"token_expired"}}`
						}
						if scenario == "banned" {
							upstream.body = `{"error":{"code":"account_deactivated"}}`
						}
						if scenario == "parse" {
							upstream.modelStatus = 500
						}
						var usage *UsageInfo
						var snapshot *domain.MonitorQuotaSnapshot
						if path == "restricted" {
							var err error
							usage, err = svc.GetUsage(upgradeUsageReader(true), id, true)
							require.NoError(t, err)
						} else {
							usage, snapshot = callQuotaRefresh(t, svc, repo, id, path, true)
						}
						if snapshot != nil {
							require.Equal(t, scenario == "success", snapshot.Success)
							require.Equal(t, scenario == "401" || scenario == "token403" || scenario == "banned", snapshot.CredentialInvalid)
							want := MonitorStatusError
							if scenario == "success" {
								want = MonitorStatusOperational
							}
							if scenario == "401" || scenario == "token403" || scenario == "banned" {
								want = MonitorStatusFailed
							}
							require.Equal(t, want, deriveQuotaCheckResult(snapshot, "quota", time.Now()).Status)
						} else if scenario == "success" {
							require.Empty(t, usage.Error)
							require.Equal(t, "active", usage.Source)
							require.NotNil(t, usage.UpdatedAt)
							want := 55.0
							if shadow {
								want = 8
							}
							if path == "restricted" && !shadow {
								want = 12
							}
							require.Equal(t, want, usage.FiveHour.Utilization)
						} else {
							require.NotEmpty(t, usage.Error)
							require.NotContains(t, usage.Error, "secret")
							require.Equal(t, "passive", usage.Source)
							if history {
								require.Zero(t, usage.FiveHour.Utilization)
								require.Equal(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
							} else {
								require.Nil(t, usage.UpdatedAt)
								require.True(t, usage.FiveHour.UtilizationUnknown)
							}
							if scenario == "401" || scenario == "token403" {
								require.True(t, usage.NeedsReauth)
							}
							if scenario == "banned" {
								require.True(t, usage.IsBanned)
							}
							if scenario == "403" {
								require.False(t, usage.NeedsReauth)
								require.False(t, usage.IsBanned)
							}
						}
						wantRequest := "POST /backend-api/codex/responses"
						if path == "restricted" || path == "monitor" || shadow {
							wantRequest = "GET /backend-api/wham/usage"
							require.Empty(t, upstream.modelLog())
						}
						require.Equal(t, []string{wantRequest}, upstream.requestLog())
						other := int64(200)
						if shadow {
							other = 100
						}
						require.Empty(t, repo.accounts[other].Extra)
					})
				}
			}
		}
	}
}

func TestOpenAIUsageRefresh_ThrottleForcePassiveAndRecovery(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(fmt.Sprint(shadow), func(t *testing.T) {
			svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
			upstream.modelStatus, upstream.status = 500, 500
			upstream.modelHeaders = nil
			failed, err := svc.GetUsage(context.Background(), id)
			require.NoError(t, err)
			require.NotEmpty(t, failed.Error)
			again, err := svc.GetUsage(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, failed.Error, again.Error)
			require.Len(t, upstream.requestLog(), 1)
			passive, err := svc.GetPassiveUsage(context.Background(), id)
			require.NoError(t, err)
			require.Equal(t, failed.UpdatedAt, passive.UpdatedAt)
			require.Equal(t, "passive", passive.Source)
			upstream.modelStatus, upstream.status = 200, 200
			upstream.modelHeaders = fallbackQuotaHeaders()
			fresh, err := svc.GetUsage(context.Background(), id, true)
			require.NoError(t, err)
			require.Empty(t, fresh.Error)
			require.Equal(t, "active", fresh.Source)
			require.Len(t, upstream.requestLog(), 2)
			_, err = svc.GetUsage(context.Background(), id)
			require.NoError(t, err)
			require.Len(t, upstream.requestLog(), 2)
			repo.accounts[id].Extra["codex_usage_updated_at"] = "2020-01-01T00:00:00Z"
			svc.cache.openAIUsageCache.Store(openAIUsageFlightKey(context.Background(), id), &openAIUsageCacheEntry{timestamp: time.Now().Add(-11 * time.Minute)})
			_, err = svc.GetUsage(context.Background(), id)
			require.NoError(t, err)
			require.Len(t, upstream.requestLog(), 3)
		})
	}
}

func TestOpenAIUsageRefresh_ModelProbeSuccess(t *testing.T) {
	for _, status := range []int{200, 429} {
		svc, _, upstream, id := quotaRefreshFixture(t, false, false)
		upstream.modelStatus = status
		usage, err := svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.Empty(t, usage.Error)
		require.Equal(t, 55.0, usage.FiveHour.Utilization)
		require.Equal(t, []string{"POST /backend-api/codex/responses"}, upstream.requestLog())
	}
}

func TestOpenAIUsageRefresh_SparkRejectsGlobalHeaders(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, true, false)
	upstream.status = 503
	usage, err := svc.GetUsage(context.Background(), id, true)
	require.NoError(t, err)
	require.NotEmpty(t, usage.Error)
	require.Empty(t, repo.writes)
	require.Empty(t, upstream.modelLog())
	require.True(t, usage.FiveHour.UtilizationUnknown)
}

func TestOpenAIUsageRefresh_PartialWindowKeepsOwnTimeAndClearsReset(t *testing.T) {
	for _, window := range []string{"5h", "7d"} {
		svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
		old := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
		for _, key := range []string{"5h", "7d"} {
			repo.accounts[id].Extra["codex_"+key+"_sampled_at"] = old.Format(time.RFC3339)
			repo.accounts[id].Extra["codex_"+key+"_reset_at"] = time.Now().Add(time.Hour).Format(time.RFC3339)
		}
		headers := http.Header{}
		headers.Set("x-codex-primary-used-percent", "40")
		minutes := "300"
		other := "7d"
		if window == "7d" {
			minutes = "10080"
			other = "5h"
		}
		headers.Set("x-codex-primary-window-minutes", minutes)
		upstream.modelHeaders = headers
		usage, err := svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.Empty(t, usage.Error)
		changed, retained := usage.FiveHour, usage.SevenDay
		if window == "7d" {
			changed, retained = usage.SevenDay, usage.FiveHour
		}
		require.Equal(t, 40.0, changed.Utilization)
		require.Nil(t, changed.ResetsAt)
		require.Nil(t, repo.accounts[id].Extra["codex_"+window+"_reset_at"])
		require.NotNil(t, changed.SampledAt)
		require.Equal(t, old, *retained.SampledAt)
		require.NotNil(t, repo.accounts[id].Extra["codex_"+other+"_reset_at"])
	}
}

func TestOpenAIUsageRefresh_MonitorExpiredFailureNeverRecovers(t *testing.T) {
	for _, expired := range []bool{false, true} {
		for _, status := range []int{200, 401, 403, 500} {
			svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
			reset := time.Now().Add(time.Hour)
			if expired {
				reset = time.Now().Add(-time.Hour)
			}
			repo.accounts[id].Extra["codex_5h_used_percent"] = 100.0
			repo.accounts[id].Extra["codex_5h_reset_at"] = reset.Format(time.RFC3339)
			upstream.status, upstream.modelStatus = status, status
			if status != http.StatusOK {
				upstream.modelHeaders = nil
			}
			page, err := svc.GetUsage(context.Background(), id, true)
			require.NoError(t, err)
			if status == http.StatusOK {
				require.Equal(t, 55.0, page.FiveHour.Utilization)
			} else {
				wantPage := 100.0
				if expired {
					wantPage = 0
				}
				require.Equal(t, wantPage, page.FiveHour.Utilization)
				require.Equal(t, "2020-01-01T00:00:00Z", page.UpdatedAt.Format(time.RFC3339))
				throttled, err := svc.GetUsage(context.Background(), id)
				require.NoError(t, err)
				require.Equal(t, page.ErrorCode, throttled.ErrorCode)
			}
			fetcher := NewChannelMonitorQuotaFetcher(svc, nil, nil, repo, nil)
			for round := 0; round < 3; round++ {
				snapshot := fetcher.Fetch(context.Background(), id)
				res := deriveQuotaCheckResult(snapshot, "quota", time.Now())
				want := MonitorStatusError
				if status == 200 {
					want = MonitorStatusOperational
				}
				if status == 401 {
					want = MonitorStatusFailed
				}
				require.Equal(t, want, res.Status)
			}
			require.Equal(t, []string{"POST /backend-api/codex/responses", "GET /backend-api/wham/usage"}, upstream.requestLog())
		}
	}
}

func TestOpenAIUsageRefresh_AuthenticationOverridesGenericFailure(t *testing.T) {
	for _, usage := range []*UsageInfo{{ErrorCode: errorCodeQuotaRefreshFailed, NeedsReauth: true}, {ErrorCode: errorCodeQuotaRefreshFailed, IsBanned: true}, {ErrorCode: "quota_unknown", NeedsReauth: true}} {
		failed, invalid, _ := usageFailureInfo(usage)
		require.True(t, failed)
		require.True(t, invalid)
	}
	failed, invalid, _ := usageFailureInfo(&UsageInfo{ErrorCode: errorCodeQuotaRefreshFailed})
	require.True(t, failed)
	require.False(t, invalid)
}

func TestOpenAIUsageRefresh_QuotaAPIMissingResetNeverBecomesZero(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
		upstream.body = `{"rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":18000}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":40,"limit_window_seconds":18000}}}]}`
		usage, err := svc.GetUsage(upgradeUsageReader(true), id, true)
		require.NoError(t, err)
		require.Empty(t, usage.Error)
		require.Equal(t, 40.0, usage.FiveHour.Utilization)
		require.Nil(t, usage.FiveHour.ResetsAt)
		require.NotNil(t, usage.FiveHour.Estimate)
		require.Nil(t, usage.FiveHour.Estimate.RemainingCost)
		require.Nil(t, repo.accounts[id].Extra["codex_5h_reset_at"])
		require.Equal(t, []string{"GET /backend-api/wham/usage"}, upstream.requestLog())
	}
}

// The old asynchronous persist helper was removed after moving every caller to
// synchronous snapshot persistence. Keep its no-scheduler-mutation contract.
func TestAccountUsageService_PersistOpenAICodexProbeSnapshotOnlyUpdatesExtra(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, false, false)
	reset := time.Now().Add(time.Hour)
	repo.accounts[id].RateLimitResetAt = &reset
	upstream.modelHeaders.Set("x-codex-primary-used-percent", "100")
	usage, err := svc.GetUsage(context.Background(), id, true)
	require.NoError(t, err)
	require.Empty(t, usage.Error)
	require.Equal(t, 100.0, usage.FiveHour.Utilization)
	require.Equal(t, []int64{id}, repo.writes)
	require.Equal(t, reset, *repo.accounts[id].RateLimitResetAt)
}

func TestOpenAIUsageRefresh_ForcedFailureRemainsVisibleWithFreshSnapshot(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		svc, repo, upstream, id := quotaRefreshFixture(t, false, false)
		fresh, err := svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.Empty(t, fresh.Error)
		upstream.modelStatus = status
		upstream.modelHeaders = nil
		failed, err := svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.NotEmpty(t, failed.Error)
		require.Equal(t, fresh.UpdatedAt, failed.UpdatedAt)
		require.Equal(t, fresh.FiveHour.Utilization, failed.FiveHour.Utilization)
		again, err := svc.GetUsage(context.Background(), id)
		require.NoError(t, err)
		require.Equal(t, failed.ErrorCode, again.ErrorCode)
		require.Equal(t, failed.Error, again.Error)
		require.Len(t, upstream.requestLog(), 2)
		// Once the attempt throttle expires it can recover, even though the
		// persisted successful observation is still inside its freshness TTL.
		entry := *svc.cachedCodexRefresh(context.Background(), id)
		entry.timestamp = time.Now().Add(-11 * time.Minute)
		svc.cache.openAIUsageCache.Store(openAIUsageFlightKey(context.Background(), id), &entry)
		upstream.modelStatus = http.StatusOK
		upstream.modelHeaders = fallbackQuotaHeaders()
		recovered, err := svc.GetUsage(context.Background(), id)
		require.NoError(t, err)
		require.Empty(t, recovered.Error)
		require.Len(t, upstream.requestLog(), 3)
		require.NotNil(t, repo.accounts[id].Extra["codex_5h_sampled_at"])
	}
}

func TestOpenAIUsageRefresh_MissingPercentIsNotZero(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		for _, history := range []bool{false, true} {
			for _, window := range []string{`{}`, `{"limit_window_seconds":18000}`, `{"used_percent":null,"limit_window_seconds":18000}`} {
				svc, repo, upstream, id := quotaRefreshFixture(t, shadow, history)
				rate := `{"primary_window":` + window + `}`
				upstream.body = `{"rate_limit":` + rate + `,"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":` + rate + `}]}`
				usage, err := svc.GetUsage(upgradeUsageReader(true), id, true)
				require.NoError(t, err)
				require.NotEmpty(t, usage.Error)
				if !history {
					require.True(t, usage.FiveHour.UtilizationUnknown)
					require.Nil(t, usage.UpdatedAt)
				}
				snapshot := NewChannelMonitorQuotaFetcher(svc, nil, nil, repo, nil).Fetch(context.Background(), id)
				require.False(t, snapshot.Success)
				require.False(t, snapshot.CredentialInvalid)
				require.Equal(t, MonitorStatusError, deriveQuotaCheckResult(snapshot, "quota", time.Now()).Status)
				require.Empty(t, repo.writes)
				require.Empty(t, upstream.modelLog())
			}
		}
	}
}
