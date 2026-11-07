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
		status, body, timeout = u.modelStatus, "", false
		if u.recoverAfterModel {
			u.status = http.StatusOK
		}
	}
	u.mu.Unlock()
	if r.Method == http.MethodGet && release != nil {
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
			"codex_usage_updated_at": "2020-01-01T00:00:00Z",
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
	upstream := &quotaRefreshUpstream{status: 200, body: string(body), modelStatus: 502}
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

func TestOpenAIUsageRefresh_AllEntryPoints(t *testing.T) {
	for _, path := range []string{"single", "batch", "monitor"} {
		for _, shadow := range []bool{false, true} {
			for _, scenario := range []string{"success", "401", "403", "429", "500", "timeout", "parse", "empty"} {
				t.Run(fmt.Sprintf("%s/shadow=%t/%s", path, shadow, scenario), func(t *testing.T) {
					t.Parallel()
					svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
					switch scenario {
					case "401":
						upstream.status = 401
					case "403":
						upstream.status = 403
					case "429":
						upstream.status = 429
					case "500":
						upstream.status = 500
					case "timeout":
						upstream.timeout = true
					case "parse":
						upstream.body = `{"rate_limit": sensitive-quota-token}`
					case "empty":
						upstream.body = `{"rate_limit":{},"additional_rate_limits":[]}`
					}
					if upstream.status != 200 {
						upstream.body = `{"error":"Bearer sensitive-quota-token"}`
					}
					usage, snapshot := callQuotaRefresh(t, svc, repo, id, path, false)
					if path == "monitor" {
						require.Equal(t, scenario == "success", snapshot.Success)
						require.Equal(t, scenario == "401" || scenario == "403", snapshot.CredentialInvalid)
						expected := MonitorStatusError
						switch scenario {
						case "success":
							expected = MonitorStatusOperational
						case "401", "403":
							expected = MonitorStatusFailed
						}
						require.Equal(t, expected, deriveQuotaCheckResult(snapshot, "", time.Now()).Status)
						require.NotContains(t, snapshot.Error, "sensitive-quota-token")
					} else if scenario == "success" {
						require.Empty(t, usage.Error)
						require.Empty(t, usage.ErrorCode)
						require.NotNil(t, usage.UpdatedAt)
						require.WithinDuration(t, time.Now(), *usage.UpdatedAt, 3*time.Second)
						if shadow {
							require.Equal(t, 8.0, usage.FiveHour.Utilization)
							require.Equal(t, 16.0, usage.SevenDay.Utilization)
						} else {
							require.Equal(t, 12.0, usage.FiveHour.Utilization)
							require.Equal(t, 24.0, usage.SevenDay.Utilization)
						}
					} else {
						require.NotEmpty(t, usage.Error)
						require.NotEmpty(t, usage.ErrorCode)
						require.NotContains(t, usage.Error, "sensitive-quota-token")
						require.Equal(t, 37.0, usage.FiveHour.Utilization)
						require.Equal(t, 63.0, usage.SevenDay.Utilization)
						require.Equal(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
						again, err := svc.GetUsage(context.Background(), id)
						require.NoError(t, err)
						require.Equal(t, usage.Error, again.Error)
						require.Equal(t, usage.UpdatedAt, again.UpdatedAt)
					}
					expectedRequests := []string{"GET /backend-api/wham/usage"}
					if scenario != "success" {
						expectedRequests = append(expectedRequests, "POST /backend-api/codex/responses")
					}
					require.Equal(t, expectedRequests, upstream.requestLog(), "read first; attempt at most one model fallback on failure")
					for _, credentials := range upstream.credentialLog() {
						require.Equal(t, "Bearer sensitive-quota-token|parent-org", credentials)
					}
					if scenario != "success" {
						model := "codex-auto-review"
						if shadow {
							model = "gpt-5.3-codex-spark"
						}
						require.Equal(t, []string{model}, upstream.modelLog())
					}
					if scenario == "success" {
						require.Equal(t, []int64{id}, repo.writes)
						other := int64(200)
						if shadow {
							other = 100
						}
						require.Empty(t, repo.accounts[other].Extra, "parent and child snapshots must remain isolated")
					} else {
						require.Empty(t, repo.writes)
					}
				})
			}
		}
	}
}

func TestOpenAIUsageRefresh_ThrottleForcePassiveAndRecovery(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		for _, ws := range []bool{false, true} {
			t.Run(fmt.Sprintf("shadow=%t/ws=%t", shadow, ws), func(t *testing.T) {
				svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
				repo.accounts[id].Extra["openai_oauth_responses_websockets_v2_enabled"] = ws
				ctx := context.Background()
				upstream.status = 503
				failed, err := svc.GetUsage(ctx, id)
				require.NoError(t, err)
				require.NotEmpty(t, failed.Error)
				upstream.mu.Lock()
				upstream.status = 200
				upstream.mu.Unlock()
				throttled, err := svc.GetUsage(ctx, id)
				require.NoError(t, err)
				require.Equal(t, failed.Error, throttled.Error)
				passive, err := svc.GetPassiveUsage(ctx, id)
				require.NoError(t, err)
				require.Equal(t, "passive", passive.Source)
				require.Equal(t, failed.UpdatedAt, passive.UpdatedAt)
				require.Equal(t, failed.Error, passive.Error)
				require.Len(t, upstream.requestLog(), 2)
				recovered, err := svc.GetUsage(ctx, id, true)
				require.NoError(t, err)
				require.Empty(t, recovered.Error)
				require.Empty(t, recovered.ErrorCode)
				require.Len(t, upstream.requestLog(), 3)
				fresh, err := svc.GetUsage(ctx, id)
				require.NoError(t, err)
				require.Equal(t, recovered.UpdatedAt, fresh.UpdatedAt)
				require.Len(t, upstream.requestLog(), 3)
				// Expire both the snapshot and throttle to exercise an automatic retry.
				repo.accounts[id].Extra["codex_usage_updated_at"] = "2020-01-01T00:00:00Z"
				previous := svc.cachedOpenAIUsage(id)
				svc.cache.openAIUsageCache.Store(id, &openAIUsageCacheEntry{timestamp: previous.timestamp.Add(-11 * time.Minute)})
				refreshed, err := svc.GetUsage(ctx, id)
				require.NoError(t, err)
				require.Empty(t, refreshed.Error)
				require.Len(t, upstream.requestLog(), 4)
			})
		}
	}
}

func TestOpenAIUsageRefresh_MissingWindowsAndTime(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		t.Run(fmt.Sprint(shadow), func(t *testing.T) {
			svc, repo, upstream, id := quotaRefreshFixture(t, shadow, false)
			ctx := context.Background()
			upstream.body = `{}`
			unknown, err := svc.GetUsage(ctx, id)
			require.NoError(t, err)
			require.Equal(t, "quota_windows_missing", unknown.ErrorCode)
			require.Nil(t, unknown.FiveHour)
			require.Nil(t, unknown.SevenDay)
			require.Nil(t, unknown.UpdatedAt)
			upstream.mu.Lock()
			if shadow {
				upstream.body = `{"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":3600}}}]}`
			} else {
				upstream.body = `{"rate_limit":{"primary_window":{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":3600}}}`
			}
			upstream.mu.Unlock()
			// Even if there was an old second window, it must not acquire the new time.
			repo.accounts[id].Extra["codex_7d_used_percent"] = 77.0
			single, err := svc.GetUsage(ctx, id, true)
			require.NoError(t, err)
			require.Empty(t, single.Error)
			require.NotNil(t, single.FiveHour)
			require.Equal(t, 0.0, single.FiveHour.Utilization)
			require.Nil(t, single.SevenDay)
			require.NotNil(t, single.UpdatedAt)
			_, err = svc.GetUsage(ctx, id)
			require.NoError(t, err)
			require.Len(t, upstream.requestLog(), 3)
			// Local snapshots without a valid observation time must leave it unknown.
			for _, stamp := range []any{nil, "", "invalid"} {
				repo.accounts[id].Extra["codex_usage_updated_at"] = stamp
				passive, err := svc.GetPassiveUsage(ctx, id)
				require.NoError(t, err)
				require.Nil(t, passive.UpdatedAt)
			}
		})
	}
}

func TestOpenAIUsageRefresh_UnconfiguredAndPersistenceFailure(t *testing.T) {
	for _, old := range []bool{false, true} {
		svc, repo, _, id := quotaRefreshFixture(t, false, old)
		quota := svc.openAIQuotaService
		svc.openAIQuotaService = nil
		failed, err := svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.Equal(t, "quota_service_unavailable", failed.ErrorCode)
		if old {
			require.Equal(t, 37.0, failed.FiveHour.Utilization)
			require.Equal(t, "2020-01-01T00:00:00Z", failed.UpdatedAt.Format(time.RFC3339))
		} else {
			require.Nil(t, failed.FiveHour)
			require.Nil(t, failed.UpdatedAt)
		}
		svc.openAIQuotaService = quota
		repo.writeErr = fmt.Errorf("sensitive storage failure")
		failed, err = svc.GetUsage(context.Background(), id, true)
		require.NoError(t, err)
		require.Equal(t, "quota_snapshot_write_failed", failed.ErrorCode)
		require.NotContains(t, failed.Error, "sensitive")
		require.Empty(t, repo.writes)
	}
}

func TestOpenAIUsageRefresh_ForcedFailureRetainsFreshSnapshot(t *testing.T) {
	svc, _, upstream, id := quotaRefreshFixture(t, false, false)
	ctx := context.Background()
	fresh, err := svc.GetUsage(ctx, id)
	require.NoError(t, err)
	require.Empty(t, fresh.Error)
	upstream.mu.Lock()
	upstream.status = 401
	upstream.mu.Unlock()
	failed, err := svc.GetUsage(ctx, id, true)
	require.NoError(t, err)
	require.Equal(t, errorCodeUnauthenticated, failed.ErrorCode)
	require.Equal(t, fresh.UpdatedAt, failed.UpdatedAt)
	require.Equal(t, fresh.FiveHour.Utilization, failed.FiveHour.Utilization)
	again, err := svc.GetUsage(ctx, id)
	require.NoError(t, err)
	require.Equal(t, failed.Error, again.Error, "a fresh snapshot must not hide the failed forced refresh")
	require.Equal(t, fresh.UpdatedAt, again.UpdatedAt)
	require.Len(t, upstream.requestLog(), 3)
	// A normal retry after the throttle expires must recover and clear the error.
	svc.cache.openAIUsageCache.Delete(id)
	svc.cache.openAIFailures.Store(openAIUsageFlightKey(ctx, id), &openAIUsageCacheEntry{timestamp: time.Now().Add(-11 * time.Minute), message: failed.Error, errorCode: failed.ErrorCode})
	upstream.mu.Lock()
	upstream.status = 200
	upstream.mu.Unlock()
	recovered, err := svc.GetUsage(ctx, id)
	require.NoError(t, err)
	require.Empty(t, recovered.Error)
	require.Empty(t, recovered.ErrorCode)
	require.Len(t, upstream.requestLog(), 4)
}

func TestOpenAIUsageRefresh_FreshPersistedSnapshotSkipsQuery(t *testing.T) {
	for _, ws := range []bool{false, true} {
		svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
		repo.accounts[id].Extra["codex_usage_updated_at"] = time.Now().UTC().Format(time.RFC3339)
		repo.accounts[id].Extra["openai_oauth_responses_websockets_v2_enabled"] = ws
		usage, err := svc.GetUsage(context.Background(), id)
		require.NoError(t, err)
		require.Empty(t, usage.Error)
		require.Equal(t, 37.0, usage.FiveHour.Utilization)
		require.NotNil(t, usage.UpdatedAt)
		require.Empty(t, upstream.requestLog())
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

func TestOpenAIUsageRefresh_ModelFallbackSuccess(t *testing.T) {
	for _, path := range []string{"single", "batch", "monitor"} {
		for _, shadow := range []bool{false, true} {
			for _, modelStatus := range []int{200, 429} {
				t.Run(fmt.Sprintf("%s/shadow=%t/status=%d", path, shadow, modelStatus), func(t *testing.T) {
					svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
					upstream.status = 503
					upstream.modelStatus = modelStatus
					upstream.modelHeaders = fallbackQuotaHeaders()
					// Spark must re-read its own bengalfox windows, even though the model
					// response carries apparently valid (but global) x-codex-* headers.
					upstream.recoverAfterModel = shadow
					usage, snapshot := callQuotaRefresh(t, svc, repo, id, path, false)
					if snapshot != nil {
						require.True(t, snapshot.Success)
						require.Empty(t, snapshot.Error)
						require.Equal(t, MonitorStatusOperational, deriveQuotaCheckResult(snapshot, "", time.Now()).Status)
					} else {
						require.Empty(t, usage.Error)
						require.Empty(t, usage.ErrorCode)
						require.NotNil(t, usage.UpdatedAt)
						require.NotEqual(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
						if shadow {
							require.Equal(t, 8.0, usage.FiveHour.Utilization)
							require.Equal(t, 16.0, usage.SevenDay.Utilization)
						} else {
							require.Equal(t, 55.0, usage.FiveHour.Utilization)
							require.Equal(t, 65.0, usage.SevenDay.Utilization)
						}
					}
					expected := []string{"GET /backend-api/wham/usage", "POST /backend-api/codex/responses"}
					model := "codex-auto-review"
					if shadow {
						expected = append(expected, "GET /backend-api/wham/usage")
						model = "gpt-5.3-codex-spark"
						require.Empty(t, repo.accounts[100].Extra, "Spark fallback must not write the parent's snapshot")
					} else {
						require.Empty(t, repo.accounts[200].Extra)
					}
					require.Equal(t, expected, upstream.requestLog())
					require.Equal(t, []string{model}, upstream.modelLog())
					for _, credentials := range upstream.credentialLog() {
						require.Equal(t, "Bearer sensitive-quota-token|parent-org", credentials)
					}
					require.Equal(t, []int64{id}, repo.writes)
					// Cache hits, including passive reads, must not send another model request.
					_, err := svc.GetUsage(context.Background(), id)
					require.NoError(t, err)
					_, err = svc.GetPassiveUsage(context.Background(), id)
					require.NoError(t, err)
					require.Equal(t, expected, upstream.requestLog())
				})
			}
		}
	}
}

func TestOpenAIUsageRefresh_SparkFallbackRejectsGlobalHeaders(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, true, true)
	upstream.status = 503
	upstream.modelStatus = 200
	upstream.modelHeaders = fallbackQuotaHeaders()
	usage, err := svc.GetUsage(context.Background(), id)
	require.NoError(t, err)
	require.NotEmpty(t, usage.Error)
	require.Equal(t, 37.0, usage.FiveHour.Utilization)
	require.Equal(t, 63.0, usage.SevenDay.Utilization)
	require.Equal(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
	require.Empty(t, repo.writes)
	require.Equal(t, []string{"GET /backend-api/wham/usage", "POST /backend-api/codex/responses", "GET /backend-api/wham/usage"}, upstream.requestLog())
	_, err = svc.GetUsage(context.Background(), id)
	require.NoError(t, err)
	require.Len(t, upstream.requestLog(), 3)
}

func TestOpenAIUsageRefresh_FallbackSingleWindowClearsOldWindow(t *testing.T) {
	svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
	upstream.status = 503
	upstream.modelStatus = 200
	upstream.modelHeaders = fallbackQuotaHeaders()
	upstream.modelHeaders.Del("x-codex-secondary-used-percent")
	upstream.modelHeaders.Del("x-codex-secondary-window-minutes")
	upstream.modelHeaders.Del("x-codex-secondary-reset-after-seconds")
	usage, err := svc.GetUsage(context.Background(), id)
	require.NoError(t, err)
	require.Empty(t, usage.Error)
	require.Equal(t, 55.0, usage.FiveHour.Utilization)
	require.Nil(t, usage.SevenDay)
	require.Nil(t, repo.accounts[id].Extra["codex_7d_used_percent"])
	require.NotNil(t, usage.UpdatedAt)
}

func TestOpenAIUsageRefresh_FallbackAuthenticationFailure(t *testing.T) {
	for _, status := range []int{401, 403} {
		svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
		upstream.status = 503
		upstream.modelStatus = status
		snapshot := NewChannelMonitorQuotaFetcher(svc, nil, nil, repo, nil).Fetch(context.Background(), id)
		require.False(t, snapshot.Success)
		require.True(t, snapshot.CredentialInvalid)
		require.Equal(t, MonitorStatusFailed, deriveQuotaCheckResult(snapshot, "", time.Now()).Status)
		require.Empty(t, repo.writes)
	}
}

func TestOpenAIUsageRefresh_FallbackRejectsInvalidPercent(t *testing.T) {
	for _, value := range []string{"NaN", "Inf"} {
		svc, repo, upstream, id := quotaRefreshFixture(t, false, true)
		upstream.status = 503
		upstream.modelStatus = 200
		upstream.modelHeaders = fallbackQuotaHeaders()
		upstream.modelHeaders.Set("x-codex-primary-used-percent", value)
		usage, err := svc.GetUsage(context.Background(), id)
		require.NoError(t, err)
		require.NotEmpty(t, usage.Error)
		require.Equal(t, 37.0, usage.FiveHour.Utilization)
		require.Equal(t, 63.0, usage.SevenDay.Utilization)
		require.Equal(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
		require.Empty(t, repo.writes)
	}
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
			require.Nil(t, svc.cachedOpenAIUsage(id), "caller cancellation must not populate the cache")
			unblock()
			got := awaitResult(survivor)
			require.NoError(t, got.err)
			require.NotNil(t, got.usage)
			require.Empty(t, got.usage.Error)
			require.Equal(t, 12.0, got.usage.FiveHour.Utilization)
			again, err := svc.GetUsage(ctx, id)
			require.NoError(t, err)
			require.Empty(t, again.Error)
			require.Equal(t, got.usage.UpdatedAt, again.UpdatedAt)
			require.Equal(t, []string{"GET /backend-api/wham/usage"}, upstream.requestLog())
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
	require.Nil(t, svc.cachedOpenAIUsage(id))
	require.Empty(t, upstream.requestLog())
}

func TestOpenAIUsageRefresh_MissingPercentIsNotZero(t *testing.T) {
	for _, path := range []string{"single", "batch", "monitor"} {
		for _, shadow := range []bool{false, true} {
			for _, old := range []bool{false, true} {
				for _, window := range []string{`{}`, `{"limit_window_seconds":18000}`, `{"used_percent":null,"limit_window_seconds":18000}`} {
					t.Run(fmt.Sprintf("%s/shadow=%t/old=%t/%s", path, shadow, old, window), func(t *testing.T) {
						svc, repo, upstream, id := quotaRefreshFixture(t, shadow, old)
						rateLimit := `{"primary_window":` + window + `,"secondary_window":` + window + `}`
						upstream.body = `{"rate_limit":` + rateLimit + `}`
						if shadow {
							upstream.body = `{"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":` + rateLimit + `}]}`
						}
						usage, monitor := callQuotaRefresh(t, svc, repo, id, path, false)
						if monitor != nil {
							require.False(t, monitor.Success)
							require.False(t, monitor.CredentialInvalid)
						} else {
							require.Equal(t, "quota_windows_missing", usage.ErrorCode)
							if old {
								require.Equal(t, 37.0, usage.FiveHour.Utilization)
								require.Equal(t, 63.0, usage.SevenDay.Utilization)
								require.Equal(t, "2020-01-01T00:00:00Z", usage.UpdatedAt.Format(time.RFC3339))
							} else {
								require.Nil(t, usage.FiveHour)
								require.Nil(t, usage.SevenDay)
								require.Nil(t, usage.UpdatedAt)
							}
						}
						require.Empty(t, repo.writes)
						require.Equal(t, []string{"GET /backend-api/wham/usage", "POST /backend-api/codex/responses"}, upstream.requestLog())
					})
				}
			}
		}
	}
}

func TestOpenAIUsageRefresh_MissingPercentUsesModelFallback(t *testing.T) {
	svc, _, upstream, id := quotaRefreshFixture(t, false, true)
	upstream.body = `{"rate_limit":{"primary_window":{}}}`
	upstream.modelStatus = http.StatusOK
	upstream.modelHeaders = fallbackQuotaHeaders()
	usage, err := svc.GetUsage(context.Background(), id)
	require.NoError(t, err)
	require.Empty(t, usage.Error)
	require.Equal(t, 55.0, usage.FiveHour.Utilization)
	require.Equal(t, 65.0, usage.SevenDay.Utilization)
	require.Equal(t, []string{"GET /backend-api/wham/usage", "POST /backend-api/codex/responses"}, upstream.requestLog())
}

func TestOpenAIUsageRefresh_ExplicitZeroWithMissingOtherWindow(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		for _, primaryZero := range []bool{false, true} {
			t.Run(fmt.Sprintf("shadow=%t/primaryZero=%t", shadow, primaryZero), func(t *testing.T) {
				svc, repo, upstream, id := quotaRefreshFixture(t, shadow, true)
				repo.accounts[id].Extra["codex_7d_sampled_at"] = "2020-01-01T00:00:00Z"
				zero := `{"used_percent":0,"limit_window_seconds":18000,"reset_after_seconds":3600}`
				missing := `{"used_percent":null,"limit_window_seconds":604800}`
				primary, secondary := zero, missing
				if !primaryZero {
					primary, secondary = secondary, primary
				}
				rateLimit := `{"allowed":true,"primary_window":` + primary + `,"secondary_window":` + secondary + `}`
				upstream.body = `{"rate_limit":` + rateLimit + `}`
				if shadow {
					upstream.body = `{"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":` + rateLimit + `}]}`
				}
				usage, err := svc.GetUsage(context.Background(), id)
				require.NoError(t, err)
				require.Empty(t, usage.Error)
				require.NotNil(t, usage.FiveHour)
				require.Zero(t, usage.FiveHour.Utilization)
				require.NotNil(t, usage.FiveHour.ResetsAt)
				require.Nil(t, usage.SevenDay)
				require.Nil(t, repo.accounts[id].Extra["codex_7d_used_percent"])
				require.Nil(t, repo.accounts[id].Extra["codex_7d_sampled_at"])
				require.Equal(t, usage.UpdatedAt, usage.FiveHour.SampledAt)
				require.NotNil(t, usage.UpdatedAt)
				require.Equal(t, []string{"GET /backend-api/wham/usage"}, upstream.requestLog())
			})
		}
	}
}
