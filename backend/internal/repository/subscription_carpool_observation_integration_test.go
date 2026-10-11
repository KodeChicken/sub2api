//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type carpoolObservationFixture struct {
	accountID      int64
	subscriptionID int64
	subscriptions  *service.SubscriptionService
	accounts       service.AccountRepository
	baselineAt     time.Time
	resetAt        time.Time
}

func newCarpoolObservationFixture(t *testing.T) *carpoolObservationFixture {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	weeklyLimit := 100.0
	user := mustCreateUser(t, client, &service.User{})
	group := mustCreateGroup(t, client, &service.Group{
		Name:             t.Name(),
		Platform:         service.PlatformOpenAI,
		SubscriptionType: service.SubscriptionTypeSubscription,
		WeeklyLimitUSD:   &weeklyLimit,
	})
	require.NoError(t, client.Group.UpdateOneID(group.ID).SetIsCarpool(true).Exec(ctx))
	account := mustCreateAccount(t, client, &service.Account{
		Name: t.Name(), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
	})
	mustBindAccountToGroup(t, client, account.ID, group.ID, 0)
	subscription := mustCreateSubscription(t, client, &service.UserSubscription{
		UserID: user.ID, GroupID: group.ID,
		DailyUsageUSD: 3, WeeklyUsageUSD: 20, MonthlyUsageUSD: 40,
	})
	subscriptions := service.NewSubscriptionService(nil, nil, nil, client, nil)
	t.Cleanup(subscriptions.Stop)
	f := &carpoolObservationFixture{
		accountID: account.ID, subscriptionID: subscription.ID, subscriptions: subscriptions,
		accounts:   NewAccountRepository(client, integrationDB, nil),
		baselineAt: time.Now().UTC().Add(-20 * time.Minute),
		resetAt:    time.Now().UTC().Add(time.Hour).Truncate(time.Second),
	}
	result, err := subscriptions.ObserveCarpoolQuotaSnapshot(ctx, account.ID, f.snapshot(80, f.baselineAt, f.resetAt), f.baselineAt)
	require.NoError(t, err)
	require.Nil(t, result)
	return f
}

func (f *carpoolObservationFixture) snapshot(used float64, observedAt, resetAt time.Time) map[string]any {
	return map[string]any{
		"codex_7d_used_percent":  used,
		"codex_7d_reset_at":      resetAt.Format(time.RFC3339),
		"codex_usage_updated_at": observedAt.Format(time.RFC3339Nano),
	}
}

func (f *carpoolObservationFixture) assertQuota(t *testing.T, weekly float64, revision, events int64) {
	t.Helper()
	var daily, actualWeekly, monthly float64
	var actualRevision, actualEvents int64
	require.NoError(t, integrationDB.QueryRow(`
SELECT daily_usage_usd, weekly_usage_usd, monthly_usage_usd, quota_reset_revision
FROM user_subscriptions WHERE id=$1`, f.subscriptionID).Scan(&daily, &actualWeekly, &monthly, &actualRevision))
	require.Equal(t, 3.0, daily)
	require.Equal(t, weekly, actualWeekly)
	require.Equal(t, 40.0, monthly)
	require.Equal(t, revision, actualRevision)
	require.NoError(t, integrationDB.QueryRow(`
SELECT COUNT(*) FROM carpool_subscription_quota_reset_events
WHERE account_id=$1 AND status='applied'`, f.accountID).Scan(&actualEvents))
	require.Equal(t, events, actualEvents)
}

func TestCarpoolObservationDisplayWritesCannotEraseReset(t *testing.T) {
	f := newCarpoolObservationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	updates := f.snapshot(0, now, now.Add(7*24*time.Hour))
	require.NoError(t, f.accounts.UpdateExtra(ctx, f.accountID, updates))
	account, err := f.accounts.GetByID(ctx, f.accountID)
	require.NoError(t, err)
	require.Equal(t, 0.0, account.Extra["codex_7d_used_percent"])
	state := account.Extra["carpool_quota_reset_state"].(map[string]any)
	require.Equal(t, 80.0, state["baseline"].(map[string]any)["codex_7d_used_percent"])

	result, err := f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, updates, now)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.AffectedCount)
	f.assertQuota(t, 0, 1, 1)
}

func TestCarpoolObservationForcedResetSurvivesRestart(t *testing.T) {
	f := newCarpoolObservationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := f.snapshot(0, now, f.resetAt)
	result, err := f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, first, now)
	require.NoError(t, err)
	require.Nil(t, result)
	require.NoError(t, f.accounts.UpdateExtra(ctx, f.accountID, first))
	f.assertQuota(t, 20, 0, 0)

	restarted := service.NewSubscriptionService(nil, nil, nil, testEntClient(t), nil)
	t.Cleanup(restarted.Stop)
	tooSoon := now.Add(29 * time.Second)
	result, err = restarted.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, f.snapshot(0, tooSoon, f.resetAt), tooSoon)
	require.NoError(t, err)
	require.Nil(t, result)
	f.assertQuota(t, 20, 0, 0)

	confirmedAt := now.Add(time.Minute)
	result, err = restarted.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, f.snapshot(0, confirmedAt, f.resetAt), confirmedAt)
	require.NoError(t, err)
	require.NotNil(t, result)
	f.assertQuota(t, 0, 1, 1)
}

func TestCarpoolObservationConcurrentChecksResetOnce(t *testing.T) {
	f := newCarpoolObservationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().UTC()
	updates := f.snapshot(0, now, now.Add(7*24*time.Hour))
	const workers = 8
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		svc := service.NewSubscriptionService(nil, nil, nil, testEntClient(t), nil)
		t.Cleanup(svc.Stop)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, updates, now)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	f.assertQuota(t, 0, 1, 1)
	_, err := integrationDB.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=7 WHERE id=$1`, f.subscriptionID)
	require.NoError(t, err)
	later := now.Add(time.Minute)
	result, err := f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, f.snapshot(0, later, now.Add(7*24*time.Hour)), later)
	require.NoError(t, err)
	require.Nil(t, result)
	f.assertQuota(t, 7, 1, 1)
}

func TestCarpoolObservationFailedBaselineWriteRollsBackAndRetries(t *testing.T) {
	f := newCarpoolObservationFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	updates := f.snapshot(0, now, now.Add(7*24*time.Hour))
	// Reject the final baseline write, after the event and quota were updated.
	_, err := integrationDB.Exec(fmt.Sprintf(`
ALTER TABLE accounts ADD CONSTRAINT carpool_observation_test_failure CHECK (
    id <> %d OR (extra->'carpool_quota_reset_state'->'baseline'->>'codex_7d_used_percent')::float IS DISTINCT FROM 0
) NOT VALID`, f.accountID))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT IF EXISTS carpool_observation_test_failure`)
		require.NoError(t, err)
	})
	_, err = f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, updates, now)
	require.Error(t, err)
	f.assertQuota(t, 20, 0, 0)
	require.NoError(t, f.accounts.UpdateExtra(ctx, f.accountID, updates))
	account, err := f.accounts.GetByID(ctx, f.accountID)
	require.NoError(t, err)
	state := account.Extra["carpool_quota_reset_state"].(map[string]any)
	require.Equal(t, 80.0, state["baseline"].(map[string]any)["codex_7d_used_percent"])

	_, err = integrationDB.Exec(`ALTER TABLE accounts DROP CONSTRAINT carpool_observation_test_failure`)
	require.NoError(t, err)
	result, err := f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, updates, now)
	require.NoError(t, err)
	require.NotNil(t, result)
	f.assertQuota(t, 0, 1, 1)
}

func TestCarpoolObservationManualResetRebasesWithoutResettingAgain(t *testing.T) {
	for _, source := range []string{service.CarpoolResetSourceManualCard, service.CarpoolResetSourceAutoCard, service.CarpoolResetSourceFallback} {
		t.Run(source, func(t *testing.T) {
			f := newCarpoolObservationFixture(t)
			ctx := context.Background()
			result, err := f.subscriptions.ResetCarpoolSubscriptionQuotas(ctx, f.accountID, source, "manual-reset")
			require.NoError(t, err)
			require.Equal(t, 1, result.AffectedCount)
			_, err = integrationDB.Exec(`UPDATE user_subscriptions SET weekly_usage_usd=4 WHERE id=$1`, f.subscriptionID)
			require.NoError(t, err)

			stale := f.baselineAt.Add(time.Minute)
			_, err = f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, f.snapshot(0, stale, f.resetAt), time.Now())
			require.NoError(t, err)
			account, err := f.accounts.GetByID(ctx, f.accountID)
			require.NoError(t, err)
			state := account.Extra["carpool_quota_reset_state"].(map[string]any)
			require.Equal(t, 80.0, state["baseline"].(map[string]any)["codex_7d_used_percent"])

			now := time.Now().UTC().Add(time.Minute)
			result, err = f.subscriptions.ObserveCarpoolQuotaSnapshot(ctx, f.accountID, f.snapshot(0, now, now.Add(7*24*time.Hour)), now)
			require.NoError(t, err)
			require.Nil(t, result)
			f.assertQuota(t, 4, 1, 1)
			account, err = f.accounts.GetByID(ctx, f.accountID)
			require.NoError(t, err)
			state = account.Extra["carpool_quota_reset_state"].(map[string]any)
			require.Equal(t, 0.0, state["baseline"].(map[string]any)["codex_7d_used_percent"])
		})
	}
}
