//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

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
	account *Account
}

func (r *temporaryDispatchAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return r.account, nil
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
