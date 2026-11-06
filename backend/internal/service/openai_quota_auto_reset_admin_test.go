package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminOpenAIAutoResetExpiryPreservesManagedStateAndReauthSwitch(t *testing.T) {
	state := &OpenAIAutoResetCreditState{
		Status: OpenAIAutoResetStatusFailed, TriggerWindow: "expiry",
		ExpiryInitialized: true, AttemptCreditHash: "original", AttemptCycleHash: "old-cycle",
		AttemptExpiresAt: "2026-11-06T05:00:00Z", AttemptConsumed: true,
		UnresolvedEarliestUntil: "unknown",
	}
	snapshot := &OpenAIRateLimitResetCredits{AvailableCount: 0}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Extra: map[string]any{
			OpenAIAutoResetCreditExpiryEnabledExtraKey: true,
			OpenAIAutoResetCreditStateExtraKey:         state,
			openaiQuotaResetCreditsKey:                 snapshot,
			"unrelated":                                "old",
		},
	}}}
	svc := &adminServiceImpl{accountRepo: repo}
	for _, input := range []*UpdateAccountInput{
		{Name: "edited", Extra: map[string]any{
			OpenAIAutoResetCreditStateExtraKey: map[string]any{"status": "success"},
			openaiQuotaResetCreditsKey:         map[string]any{"available_count": 100},
		}},
		{Credentials: map[string]any{"access_token": "local-test-only"}, Extra: map[string]any{"reauth": true}},
	} {
		account, err := svc.UpdateAccount(context.Background(), 1, input)
		require.NoError(t, err)
		require.True(t, ResolveOpenAIAutoResetCreditConfig(account).ExpiryEnabled)
		require.Equal(t, state, openAIAutoResetStateFromExtra(account.Extra))
		require.Equal(t, snapshot, account.Extra[openaiQuotaResetCreditsKey])
		require.NotContains(t, account.Extra, "unrelated", "extra must remain replacement, not global merge")
	}
	disabled, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
		Extra: map[string]any{OpenAIAutoResetCreditExpiryEnabledExtraKey: false},
	})
	require.NoError(t, err)
	require.False(t, ResolveOpenAIAutoResetCreditConfig(disabled).ExpiryEnabled)
	require.Equal(t, state, openAIAutoResetStateFromExtra(disabled.Extra))
	enabled, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
		Extra: map[string]any{OpenAIAutoResetCreditExpiryEnabledExtraKey: true},
	})
	require.NoError(t, err)
	reinitialized := openAIAutoResetStateFromExtra(enabled.Extra)
	require.False(t, reinitialized.ExpiryInitialized)
	require.Equal(t, state.AttemptCreditHash, reinitialized.AttemptCreditHash)
	require.Equal(t, state.AttemptCycleHash, reinitialized.AttemptCycleHash)
	require.True(t, reinitialized.AttemptConsumed)
	require.Equal(t, "unknown", reinitialized.UnresolvedEarliestUntil)
}

func TestAdminOpenAIAutoResetExpiryImportStripsRuntimeOnly(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{}
	account, err := (&adminServiceImpl{accountRepo: repo}).CreateAccount(context.Background(), &CreateAccountInput{
		Name: "imported", Platform: PlatformOpenAI, Type: AccountTypeOAuth, SkipDefaultGroupBind: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditExpiryEnabledExtraKey: true,
			OpenAIAutoResetCreditStateExtraKey: &OpenAIAutoResetCreditState{
				Status: OpenAIAutoResetStatusSuccess, ExpiryInitialized: true, AttemptCreditHash: "forged", AttemptConsumed: true,
			},
			openaiQuotaResetCreditsKey: &OpenAIRateLimitResetCredits{AvailableCount: 0},
		},
	})
	require.NoError(t, err)
	require.True(t, ResolveOpenAIAutoResetCreditConfig(account).ExpiryEnabled)
	require.NotContains(t, account.Extra, OpenAIAutoResetCreditStateExtraKey)
	require.NotContains(t, account.Extra, openaiQuotaResetCreditsKey)
}

func TestAdminOpenAIAutoResetExpiryMergeAndBulkCannotChangeConfigOrState(t *testing.T) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{},
	}}}
	svc := &adminServiceImpl{accountRepo: repo}
	forgedExtra := func() map[string]any {
		return map[string]any{
			OpenAIAutoResetCreditExpiryEnabledExtraKey: true,
			OpenAIAutoResetCreditStateExtraKey:         &OpenAIAutoResetCreditState{ExpiryInitialized: true},
			openaiQuotaResetCreditsKey:                 &OpenAIRateLimitResetCredits{AvailableCount: 0},
			"custom":                                   "retained",
		}
	}
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 1, forgedExtra()))
	require.Equal(t, map[string]any{"custom": "retained"}, repo.updates[1][0])
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: forgedExtra()})
	require.NoError(t, err)
	require.Equal(t, 1, result.Success)
	require.Equal(t, map[string]any{"custom": "retained"}, repo.bulkUpdates[0].Extra)
}

func TestAdminOpenAIAutoResetExpiryEnablingNotifiesOnlyOnce(t *testing.T) {
	notifier := &OpenAIQuotaAutoResetService{ctx: context.Background(), queue: make(chan int64, 8)}
	setOpenAIAutoResetNotifier(notifier)
	t.Cleanup(func() { clearOpenAIAutoResetNotifier(notifier) })
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: {
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
	}}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
		Extra: map[string]any{OpenAIAutoResetCreditExpiryEnabledExtraKey: true},
	})
	require.NoError(t, err)
	require.Len(t, notifier.queue, 1)
	require.Equal(t, int64(1), <-notifier.queue)
	notifier.pending.Delete(int64(1))
	for _, input := range []*UpdateAccountInput{
		{Name: "ordinary edit"},
		{Extra: map[string]any{"reauth": true}},
		{Extra: map[string]any{OpenAIAutoResetCreditExpiryEnabledExtraKey: true}},
	} {
		_, err = svc.UpdateAccount(context.Background(), 1, input)
		require.NoError(t, err)
		require.Empty(t, notifier.queue)
	}
}
