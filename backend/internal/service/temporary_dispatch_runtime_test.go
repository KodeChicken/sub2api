//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type temporaryDispatchQuotaStub struct {
	usage *OpenAIQuotaUsage
	err   error
	calls int
}

type temporaryDispatchConcurrentQuotaStub struct {
	mu      sync.Mutex
	active  int
	maximum int
	started chan struct{}
	release chan struct{}
}

func (s *temporaryDispatchConcurrentQuotaStub) QueryRateLimitUsage(ctx context.Context, _ int64) (*OpenAIQuotaUsage, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maximum {
		s.maximum = s.active
	}
	s.mu.Unlock()
	s.started <- struct{}{}
	select {
	case <-ctx.Done():
	case <-s.release:
	}
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return nil, nil
}

func (s *temporaryDispatchConcurrentQuotaStub) maxActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maximum
}

func (s *temporaryDispatchQuotaStub) QueryRateLimitUsage(_ context.Context, _ int64) (*OpenAIQuotaUsage, error) {
	s.calls++
	return s.usage, s.err
}

type temporaryDispatchStoreStub struct {
	created              *TemporaryDispatchCreateSpec
	stopped              []int64
	observed             []string
	observedUsed         []float64
	completedIDs         []int64
	cleanupIDs           []int64
	claimedIDs           []int64
	unavailableAccountID int64
	unavailableIDs       []int64
	unavailableCalled    chan int64
}

type temporaryDispatchCostStoreStub struct {
	*temporaryDispatchStoreStub
	costGroupIDs []int64
	costCalls    int
}

func (s *temporaryDispatchCostStoreStub) ObserveTemporaryDispatchCosts(_ context.Context, _ time.Time, _ int) ([]int64, error) {
	s.costCalls++
	return append([]int64(nil), s.costGroupIDs...), nil
}

func (s *temporaryDispatchStoreStub) CreateTemporaryDispatch(_ context.Context, spec TemporaryDispatchCreateSpec) error {
	copy := spec
	s.created = &copy
	return nil
}

func (s *temporaryDispatchStoreStub) StopTemporaryDispatchGroups(_ context.Context, groupIDs []int64) ([]int64, error) {
	s.stopped = append([]int64(nil), groupIDs...)
	return groupIDs, nil
}

func (s *temporaryDispatchStoreStub) ObserveTemporaryDispatchQuota(_ context.Context, _ int64, window string, used float64, _ time.Time) ([]int64, error) {
	s.observed = append(s.observed, window)
	s.observedUsed = append(s.observedUsed, used)
	return append([]int64(nil), s.completedIDs...), nil
}

func (s *temporaryDispatchStoreStub) CleanupTemporaryDispatches(_ context.Context, _ time.Time, _ int) ([]int64, error) {
	return append([]int64(nil), s.cleanupIDs...), nil
}

func (s *temporaryDispatchStoreStub) ClaimStaleTemporaryDispatchAccounts(_ context.Context, _, _ time.Time, _ int) ([]int64, error) {
	return append([]int64(nil), s.claimedIDs...), nil
}

func (s *temporaryDispatchStoreStub) StopTemporaryDispatchAccount(_ context.Context, accountID int64) ([]int64, error) {
	s.unavailableAccountID = accountID
	if s.unavailableCalled != nil {
		s.unavailableCalled <- accountID
	}
	return append([]int64(nil), s.unavailableIDs...), nil
}

type temporaryDispatchInvalidatorStub struct {
	ids    []int64
	called chan int64
}

func (s *temporaryDispatchInvalidatorStub) InvalidateAuthCacheByKey(context.Context, string)   {}
func (s *temporaryDispatchInvalidatorStub) InvalidateAuthCacheByUserID(context.Context, int64) {}

func (s *temporaryDispatchInvalidatorStub) InvalidateAuthCacheByGroupID(_ context.Context, id int64) {
	s.ids = append(s.ids, id)
	if s.called != nil {
		s.called <- id
	}
}

func temporaryDispatchQuotaUsage(now time.Time, used5h, used7d float64) *OpenAIQuotaUsage {
	return &OpenAIQuotaUsage{RateLimit: &OpenAIRateLimit{
		PrimaryWindow: &OpenAIRateLimitWindow{
			UsedPercent: used5h, LimitWindowSeconds: 5 * 60 * 60,
			ResetAt: now.Add(2 * time.Hour).Unix(),
		},
		SecondaryWindow: &OpenAIRateLimitWindow{
			UsedPercent: used7d, LimitWindowSeconds: 7 * 24 * 60 * 60,
			ResetAt: now.Add(3 * 24 * time.Hour).Unix(),
		},
	}}
}

func TestTemporaryDispatchQuotaPlanDefaultsTo5h(t *testing.T) {
	now := time.Now().UTC()
	quota := &temporaryDispatchQuotaStub{usage: temporaryDispatchQuotaUsage(now, 35, 48)}
	runtime := NewTemporaryDispatchRuntime(&temporaryDispatchStoreStub{}, quota, nil)

	plan, err := runtime.PrepareQuotaPlan(context.Background(), 42, "", 30)

	require.NoError(t, err)
	require.Equal(t, TemporaryDispatchQuotaWindow5h, plan.Window)
	require.InDelta(t, 35, plan.BaselinePercent, 0.001)
	require.InDelta(t, 65, plan.TargetPercent, 0.001)
	require.Equal(t, 1, quota.calls)
}

func TestTemporaryDispatchQuotaTargetPlanUsesAbsoluteTarget(t *testing.T) {
	now := time.Now().UTC()
	quota := &temporaryDispatchQuotaStub{usage: temporaryDispatchQuotaUsage(now, 35, 48)}
	runtime := NewTemporaryDispatchRuntime(&temporaryDispatchStoreStub{}, quota, nil)

	plan, err := runtime.PrepareQuotaTargetPlan(context.Background(), 42, "", 80)

	require.NoError(t, err)
	require.Equal(t, TemporaryDispatchQuotaWindow5h, plan.Window)
	require.InDelta(t, 35, plan.BaselinePercent, 0.001)
	require.InDelta(t, 80, plan.TargetPercent, 0.001)
	require.Equal(t, 1, quota.calls)
}

func TestTemporaryDispatchQuotaTargetPlanRejectsReachedTarget(t *testing.T) {
	now := time.Now().UTC()
	quota := &temporaryDispatchQuotaStub{usage: temporaryDispatchQuotaUsage(now, 86, 48)}
	runtime := NewTemporaryDispatchRuntime(&temporaryDispatchStoreStub{}, quota, nil)

	_, err := runtime.PrepareQuotaTargetPlan(context.Background(), 42, "", 80)

	require.Error(t, err)
}

func TestTemporaryDispatchScannerObservesAccountCostAndInvalidatesGroups(t *testing.T) {
	store := &temporaryDispatchCostStoreStub{
		temporaryDispatchStoreStub: &temporaryDispatchStoreStub{},
		costGroupIDs:               []int64{7, 8},
	}
	invalidator := &temporaryDispatchInvalidatorStub{}
	runtime := NewTemporaryDispatchRuntime(store, nil, invalidator)

	runtime.scanOnce()

	require.Equal(t, 1, store.costCalls)
	require.ElementsMatch(t, []int64{7, 8}, invalidator.ids)
}

func TestTemporaryDispatchQuotaPlanSupports7dAndRejectsOverflow(t *testing.T) {
	now := time.Now().UTC()
	quota := &temporaryDispatchQuotaStub{usage: temporaryDispatchQuotaUsage(now, 35, 85)}
	runtime := NewTemporaryDispatchRuntime(&temporaryDispatchStoreStub{}, quota, nil)

	_, err := runtime.PrepareQuotaPlan(context.Background(), 42, TemporaryDispatchQuotaWindow7d, 30)

	require.Error(t, err)
}

func TestAdminTemporaryDispatchHybridUsesEarlierQuotaReset(t *testing.T) {
	now := time.Now().UTC()
	store := &temporaryDispatchStoreStub{}
	quota := &temporaryDispatchQuotaStub{usage: temporaryDispatchQuotaUsage(now, 20, 30)}
	runtime := NewTemporaryDispatchRuntime(store, quota, nil)
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	accountRepo := &temporaryDispatchAccountRepoStub{account: &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
	}}
	svc := &adminServiceImpl{
		cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo,
		temporaryDispatchRuntime: runtime,
	}

	result, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs: []int64{7}, AccountID: 42, Mode: TemporaryDispatchModeHybrid,
		DurationMinutes: 300, QuotaWindow: TemporaryDispatchQuotaWindow5h, TargetPercent: 50,
	})

	require.NoError(t, err)
	require.NotNil(t, store.created)
	require.Equal(t, TemporaryDispatchModeHybrid, store.created.Mode)
	require.Equal(t, TemporaryDispatchQuotaWindow5h, result.QuotaWindow)
	require.InDelta(t, 20, *result.BaselinePercent, 0.001)
	require.InDelta(t, 50, *result.TargetPercent, 0.001)
	require.WithinDuration(t, now.Add(2*time.Hour), result.ExpiresAt, 2*time.Second)
}

func TestTemporaryDispatchObservationInvalidatesCompletedGroups(t *testing.T) {
	store := &temporaryDispatchStoreStub{completedIDs: []int64{7, 8}}
	invalidator := &temporaryDispatchInvalidatorStub{}
	runtime := NewTemporaryDispatchRuntime(store, nil, invalidator)
	used5h, used7d := 66.0, 44.0
	window5h, window7d := 300, 10080
	snapshot := &OpenAICodexUsageSnapshot{
		PrimaryUsedPercent: &used5h, PrimaryWindowMinutes: &window5h,
		SecondaryUsedPercent: &used7d, SecondaryWindowMinutes: &window7d,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	runtime.observeSnapshot(42, snapshot)

	require.Equal(t, []string{"5h", "7d"}, store.observed)
	require.Equal(t, []float64{66, 44}, store.observedUsed)
	require.Equal(t, []int64{7, 8, 7, 8}, invalidator.ids)
}

func TestTemporaryDispatchUnavailableAccountCleanupInvalidatesGroups(t *testing.T) {
	store := &temporaryDispatchStoreStub{
		unavailableIDs:    []int64{7, 8},
		unavailableCalled: make(chan int64, 1),
	}
	invalidator := &temporaryDispatchInvalidatorStub{called: make(chan int64, 2)}
	runtime := NewTemporaryDispatchRuntime(store, nil, invalidator)
	runtime.Start()
	t.Cleanup(runtime.Stop)

	runtime.NotifyAccountUnavailable(42, "quota_paused")
	require.Equal(t, int64(42), <-store.unavailableCalled)
	require.Equal(t, int64(7), <-invalidator.called)
	require.Equal(t, int64(8), <-invalidator.called)
	require.Equal(t, int64(42), store.unavailableAccountID)
	require.Equal(t, []int64{7, 8}, invalidator.ids)
}

func TestTemporaryDispatchQuotaPollUsesBoundedConcurrency(t *testing.T) {
	quota := &temporaryDispatchConcurrentQuotaStub{
		started: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
	runtime := NewTemporaryDispatchRuntime(&temporaryDispatchStoreStub{}, quota, nil)
	done := make(chan struct{})
	go func() {
		runtime.pollClaimedAccounts([]int64{1, 2, 3, 4, 5, 6, 7, 8}, time.Now().UTC())
		close(done)
	}()

	for range temporaryDispatchPollWorkers {
		<-quota.started
	}
	select {
	case <-quota.started:
		t.Fatal("quota poll exceeded its worker limit")
	case <-time.After(50 * time.Millisecond):
	}
	require.Equal(t, temporaryDispatchPollWorkers, quota.maxActive())

	close(quota.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quota poll workers did not stop")
	}
}
