//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type temporaryDispatchQuotaByAccountStub struct {
	usage map[int64]*OpenAIQuotaUsage
}

func (s *temporaryDispatchQuotaByAccountStub) QueryRateLimitUsage(_ context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	usage := s.usage[accountID]
	if usage == nil {
		return nil, fmt.Errorf("missing quota for account %d", accountID)
	}
	return usage, nil
}

func temporaryDispatchTestGroup(accountID int64, expiresAt time.Time) *Group {
	return &Group{
		ID:                         7,
		Name:                       "drain-group",
		Platform:                   PlatformAnthropic,
		Status:                     StatusActive,
		Hydrated:                   true,
		TemporaryDispatchAccountID: &accountID,
		TemporaryDispatchID:        "td_test",
		TemporaryDispatchExpiresAt: &expiresAt,
	}
}

func TestTemporaryDispatchAccountPoolFiltersEachAccountDeadline(t *testing.T) {
	now := time.Now().UTC()
	group := temporaryDispatchTestGroup(98, now.Add(time.Hour))
	group.TemporaryDispatchAccountIDs = []int64{98, 99}
	group.TemporaryDispatchAccountDeadlines = map[string]time.Time{
		"98": now.Add(-time.Second),
		"99": now.Add(time.Hour),
	}

	require.Equal(t, []int64{99}, group.TemporaryDispatchAccountPoolAt(now))
	require.True(t, group.HasActiveTemporaryDispatch(now))
}

func TestGatewayTemporaryDispatchSelectsUnboundTargetBeforeNormalPool(t *testing.T) {
	now := time.Now()
	target := Account{ID: 99, Name: "drain", Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100}
	normal := Account{ID: 1, Name: "normal", Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{normal, target},
		accountsByID: map[int64]*Account{1: &normal, 99: &target},
	}
	group := temporaryDispatchTestGroup(target.ID, now.Add(time.Hour))
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cfg: testConfig()}

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, temporaryDispatchInt64Ptr(7), "sticky-session", "", nil)
	require.NoError(t, err)
	require.Equal(t, target.ID, selected.ID)
}

func TestGatewayTemporaryDispatchExpiryRestoresNormalPool(t *testing.T) {
	now := time.Now()
	target := Account{ID: 99, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100}
	normal := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{normal, target},
		accountsByID: map[int64]*Account{1: &normal, 99: &target},
	}
	group := temporaryDispatchTestGroup(target.ID, now.Add(-time.Minute))
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cfg: testConfig()}

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, temporaryDispatchInt64Ptr(7), "", "", nil)
	require.NoError(t, err)
	require.Equal(t, normal.ID, selected.ID)
}

func TestGatewayTemporaryDispatchDoesNotFailOverWithinRequest(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{ID: 99, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1}
	repo := &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{99: &target}}
	group := temporaryDispatchTestGroup(target.ID, expires)
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cfg: testConfig()}

	_, err := svc.SelectAccountForModelWithExclusions(ctx, temporaryDispatchInt64Ptr(7), "", "", map[int64]struct{}{99: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestGatewayTemporaryDispatchPoolUsesOnlySelectedAccounts(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	first := Account{ID: 98, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}
	second := Account{ID: 99, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}
	normal := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 100, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{normal},
		accountsByID: map[int64]*Account{
			1: &normal, 98: &first, 99: &second,
		},
	}
	group := temporaryDispatchTestGroup(first.ID, expires)
	group.TemporaryDispatchAccountIDs = []int64{first.ID, second.ID}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &GatewayService{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}}, cfg: testConfig()}

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, temporaryDispatchInt64Ptr(7), "", "", map[int64]struct{}{first.ID: {}})
	require.NoError(t, err)
	require.Equal(t, second.ID, selected.ID)
}

func TestOpenAITemporaryDispatchSelectsTargetOutsideOriginalGroup(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{
		ID:          99,
		Name:        "openai-drain",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	repo := &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{99: &target}}
	group := temporaryDispatchTestGroup(target.ID, expires)
	group.Platform = PlatformOpenAI
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &OpenAIGatewayService{accountRepo: repo}

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, temporaryDispatchInt64Ptr(7), "old-sticky", "", nil)
	require.NoError(t, err)
	require.Equal(t, target.ID, selected.ID)
}

func TestGatewayTemporaryDispatchConcurrencyOverflowUsesOriginalGroup(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{ID: 99, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1}
	normal := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{normal, target},
		accountsByID: map[int64]*Account{
			normal.ID: &normal, target.ID: &target,
		},
	}
	group := temporaryDispatchTestGroup(target.ID, expires)
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	cache := &mockConcurrencyCache{acquireResults: map[int64]bool{target.ID: false, normal.ID: true}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}},
		cfg: cfg, concurrencyService: NewConcurrencyService(cache),
	}

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, temporaryDispatchInt64Ptr(7), "", "", nil, "", 0)

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, normal.ID, selection.Account.ID)
	require.Nil(t, selection.WaitPlan)
	require.True(t, group.HasActiveTemporaryDispatch(time.Now()), "overflow must not end the dispatch")
}

func TestGatewayTemporaryDispatchPoolUsesFreeTargetBeforeOverflow(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	full := Account{ID: 98, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1}
	free := Account{ID: 99, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1}
	normal := Account{ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{normal, full, free},
		accountsByID: map[int64]*Account{
			normal.ID: &normal, full.ID: &full, free.ID: &free,
		},
	}
	group := temporaryDispatchTestGroup(full.ID, expires)
	group.TemporaryDispatchAccountIDs = []int64{full.ID, free.ID}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	cache := &mockConcurrencyCache{
		acquireResults: map[int64]bool{full.ID: false, free.ID: true, normal.ID: true},
		loadMap: map[int64]*AccountLoadInfo{
			full.ID: {AccountID: full.ID, CurrentConcurrency: 1, LoadRate: 100},
			free.ID: {AccountID: free.ID, CurrentConcurrency: 0, LoadRate: 0},
		},
	}
	stickyCache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"temporary-sticky": full.ID}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{7: group}},
		cache: stickyCache, cfg: cfg, concurrencyService: NewConcurrencyService(cache),
	}

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, temporaryDispatchInt64Ptr(7), "temporary-sticky", "", nil, "", 0)

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, free.ID, selection.Account.ID)
}

func TestOpenAITemporaryDispatchConcurrencyOverflowUsesOriginalGroup(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}
	normal := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{normal, target},
		accountsByID: map[int64]*Account{
			normal.ID: &normal, target.ID: &target,
		},
	}
	group := temporaryDispatchTestGroup(target.ID, expires)
	group.Platform = PlatformOpenAI
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &OpenAIGatewayService{
		accountRepo: repo, cfg: testConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{acquireResults: map[int64]bool{target.ID: false, normal.ID: true}}),
	}

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, temporaryDispatchInt64Ptr(7), "", "", nil)

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, normal.ID, selection.Account.ID)
}

func TestOpenAIAdvancedTemporaryDispatchConcurrencyOverflowUsesOriginalGroup(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}
	normal := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{normal, target},
		accountsByID: map[int64]*Account{
			normal.ID: &normal, target.ID: &target,
		},
	}
	group := temporaryDispatchTestGroup(target.ID, expires)
	group.Platform = PlatformOpenAI
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &OpenAIGatewayService{
		accountRepo: repo, cfg: testConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{acquireResults: map[int64]bool{target.ID: false, normal.ID: true}}),
	}

	selection, _, err := svc.SelectAccountWithScheduler(ctx, temporaryDispatchInt64Ptr(7), "", "", "", nil, OpenAIUpstreamTransportAny, false)

	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, normal.ID, selection.Account.ID)
}

func TestTemporaryDispatchConcurrencyOverflowKeepsWaitWhenOriginalPoolHasNoDistinctAccount(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	target := Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}}
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{target}, accountsByID: map[int64]*Account{target.ID: &target},
	}
	group := temporaryDispatchTestGroup(target.ID, expires)
	group.Platform = PlatformOpenAI
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	svc := &OpenAIGatewayService{
		accountRepo: repo, cfg: testConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{acquireResults: map[int64]bool{target.ID: false}}),
	}

	selection, err := svc.SelectAccountWithLoadAwareness(ctx, temporaryDispatchInt64Ptr(7), "", "", nil)

	require.NoError(t, err)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, target.ID, selection.WaitPlan.AccountID)
}

func temporaryDispatchInt64Ptr(value int64) *int64 { return &value }

type temporaryDispatchGroupRepoStub struct {
	GroupRepository
	groups        map[int64]*Group
	setGroupIDs   []int64
	setAccountID  int64
	clearGroupIDs []int64
}

func (r *temporaryDispatchGroupRepoStub) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	group, ok := r.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	cloned := *group
	return &cloned, nil
}

func (r *temporaryDispatchGroupRepoStub) SetTemporaryDispatch(_ context.Context, groupIDs []int64, accountID int64, _ string, _, _ time.Time) error {
	r.setGroupIDs = append([]int64(nil), groupIDs...)
	r.setAccountID = accountID
	return nil
}

func (r *temporaryDispatchGroupRepoStub) ClearTemporaryDispatch(_ context.Context, groupIDs []int64) error {
	r.clearGroupIDs = append([]int64(nil), groupIDs...)
	return nil
}

type temporaryDispatchAccountRepoStub struct {
	AccountRepository
	account  *Account
	accounts map[int64]*Account
}

func (r *temporaryDispatchAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if account := r.accounts[id]; account != nil {
		return account, nil
	}
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return r.account, nil
}

func (r *temporaryDispatchAccountRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		account, err := r.GetByID(context.Background(), id)
		if err != nil {
			continue
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

func TestAdminTemporaryDispatchStartsAndStopsWithoutChangingBindings(t *testing.T) {
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformOpenAI, Status: StatusActive},
		8: {ID: 8, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	accountRepo := &temporaryDispatchAccountRepoStub{account: &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}}
	svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo}

	result, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs:        []int64{7, 8, 7},
		AccountID:       42,
		DurationMinutes: 30,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, groupRepo.setGroupIDs)
	require.Equal(t, int64(42), groupRepo.setAccountID)
	require.WithinDuration(t, result.StartedAt.Add(30*time.Minute), result.ExpiresAt, time.Second)

	require.NoError(t, svc.StopTemporaryDispatch(context.Background(), []int64{8, 7}))
	require.Equal(t, []int64{8, 7}, groupRepo.clearGroupIDs)
}

func TestAdminTemporaryDispatchCreatesIndependentAccountDeadlines(t *testing.T) {
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	first := &Account{ID: 41, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	second := &Account{ID: 42, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	accountRepo := &temporaryDispatchAccountRepoStub{accounts: map[int64]*Account{41: first, 42: second}}
	store := &temporaryDispatchStoreStub{}
	runtime := NewTemporaryDispatchRuntime(store, nil, nil)
	svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo, temporaryDispatchRuntime: runtime}

	result, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs: []int64{7},
		Mode:     TemporaryDispatchModeTime,
		Accounts: []TemporaryDispatchAccountInput{
			{AccountID: 41, DurationMinutes: 15},
			{AccountID: 42, DurationMinutes: 90},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, store.created)
	require.Len(t, store.created.Accounts, 2)
	require.WithinDuration(t, store.created.StartedAt.Add(15*time.Minute), store.created.Accounts[0].ExpiresAt, time.Second)
	require.WithinDuration(t, store.created.StartedAt.Add(90*time.Minute), store.created.Accounts[1].ExpiresAt, time.Second)
	require.WithinDuration(t, store.created.Accounts[1].ExpiresAt, result.ExpiresAt, time.Second)
	require.Equal(t, []int64{41, 42}, []int64{result.Accounts[0].AccountID, result.Accounts[1].AccountID})
}

func TestAdminTemporaryDispatchCreatesIndependentQuotaTargets(t *testing.T) {
	now := time.Now().UTC()
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	first := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	second := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}
	accountRepo := &temporaryDispatchAccountRepoStub{accounts: map[int64]*Account{41: first, 42: second}}
	store := &temporaryDispatchStoreStub{}
	quota := &temporaryDispatchQuotaByAccountStub{usage: map[int64]*OpenAIQuotaUsage{
		41: temporaryDispatchQuotaUsage(now, 10, 20),
		42: temporaryDispatchQuotaUsage(now, 30, 40),
	}}
	runtime := NewTemporaryDispatchRuntime(store, quota, nil)
	svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo, temporaryDispatchRuntime: runtime}

	result, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs: []int64{7}, Mode: TemporaryDispatchModeHybrid,
		Accounts: []TemporaryDispatchAccountInput{
			{AccountID: 41, DurationMinutes: 60, QuotaWindow: TemporaryDispatchQuotaWindow5h, TargetDeltaPercent: 15},
			{AccountID: 42, DurationMinutes: 120, QuotaWindow: TemporaryDispatchQuotaWindow7d, TargetDeltaPercent: 25},
		},
	})

	require.NoError(t, err)
	require.Len(t, result.Accounts, 2)
	require.Equal(t, TemporaryDispatchQuotaWindow5h, result.Accounts[0].QuotaWindow)
	require.InDelta(t, 10, *result.Accounts[0].BaselinePercent, 0.001)
	require.InDelta(t, 25, *result.Accounts[0].TargetPercent, 0.001)
	require.Equal(t, TemporaryDispatchQuotaWindow7d, result.Accounts[1].QuotaWindow)
	require.InDelta(t, 40, *result.Accounts[1].BaselinePercent, 0.001)
	require.InDelta(t, 65, *result.Accounts[1].TargetPercent, 0.001)
}

func TestAdminTemporaryDispatchCreatesAccountCostTargetForAPIKeyUpstream(t *testing.T) {
	rateMultiplier := 0.04
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformOpenAI, Status: StatusActive},
	}}
	account := &Account{
		ID: 42, Name: "0.04x upstream", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, RateMultiplier: &rateMultiplier,
	}
	accountRepo := &temporaryDispatchAccountRepoStub{accounts: map[int64]*Account{account.ID: account}}
	store := &temporaryDispatchStoreStub{}
	runtime := NewTemporaryDispatchRuntime(store, nil, nil)
	svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo, temporaryDispatchRuntime: runtime}

	result, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs: []int64{7}, Mode: TemporaryDispatchModeUsage,
		Accounts: []TemporaryDispatchAccountInput{{AccountID: account.ID, TargetCost: 200}},
	})

	require.NoError(t, err)
	require.NotNil(t, store.created)
	require.Len(t, store.created.Accounts, 1)
	member := store.created.Accounts[0]
	require.Equal(t, TemporaryDispatchUsageAccountCost, member.UsageMetric)
	require.Equal(t, TemporaryDispatchQuotaWindow5h, member.QuotaWindow)
	require.InDelta(t, 0, *member.BaselinePercent, 0.000001)
	require.InDelta(t, 200, *member.TargetPercent, 0.000001)
	require.InDelta(t, 0, *member.CurrentPercent, 0.000001)
	require.WithinDuration(t, store.created.StartedAt.Add(5*time.Hour), member.ExpiresAt, time.Second)
	require.Equal(t, TemporaryDispatchUsageAccountCost, result.Accounts[0].UsageMetric)
}

func TestAdminTemporaryDispatchRejectsDuplicatePoolAccounts(t *testing.T) {
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	account := &Account{ID: 41, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true}
	svc := &adminServiceImpl{
		cfg: testConfig(), groupRepo: groupRepo,
		accountRepo: &temporaryDispatchAccountRepoStub{accounts: map[int64]*Account{41: account}},
	}

	_, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{
		GroupIDs: []int64{7}, Mode: TemporaryDispatchModeTime,
		Accounts: []TemporaryDispatchAccountInput{{AccountID: 41}, {AccountID: 41}},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "DUPLICATE_TEMPORARY_DISPATCH_ACCOUNT")
}

func TestAdminTemporaryDispatchRejectsMixedPlatforms(t *testing.T) {
	groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{
		7: {ID: 7, Platform: PlatformOpenAI, Status: StatusActive},
		8: {ID: 8, Platform: PlatformAnthropic, Status: StatusActive},
	}}
	accountRepo := &temporaryDispatchAccountRepoStub{account: &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}}
	svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo}

	_, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{GroupIDs: []int64{7, 8}, AccountID: 42})
	require.Error(t, err)
	require.Empty(t, groupRepo.setGroupIDs)
}

func TestAdminTemporaryDispatchPreservesGroupAccountPolicies(t *testing.T) {
	tests := []struct {
		name  string
		group *Group
	}{
		{
			name: "oauth only",
			group: &Group{
				ID:               7,
				Platform:         PlatformOpenAI,
				Status:           StatusActive,
				RequireOAuthOnly: true,
			},
		},
		{
			name: "privacy required",
			group: &Group{
				ID:                7,
				Platform:          PlatformOpenAI,
				Status:            StatusActive,
				RequirePrivacySet: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupRepo := &temporaryDispatchGroupRepoStub{groups: map[int64]*Group{7: tt.group}}
			accountRepo := &temporaryDispatchAccountRepoStub{account: &Account{
				ID:          42,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Status:      StatusActive,
				Schedulable: true,
			}}
			svc := &adminServiceImpl{cfg: testConfig(), groupRepo: groupRepo, accountRepo: accountRepo}

			_, err := svc.StartTemporaryDispatch(context.Background(), StartTemporaryDispatchInput{GroupIDs: []int64{7}, AccountID: 42})

			require.Error(t, err)
			require.Empty(t, groupRepo.setGroupIDs)
		})
	}
}
