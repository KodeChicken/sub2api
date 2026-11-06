package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newExpiryAutoResetFixture(t *testing.T, remaining ...time.Duration) (*OpenAIQuotaAutoResetService, *autoResetTestAccountRepo, *autoResetTestQuota, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 6, 4, 0, 0, 123456789, time.UTC)
	credits := &OpenAIRateLimitResetCredits{AvailableCount: len(remaining)}
	candidates := make([]openAIAutoResetCreditCandidate, 0, len(remaining))
	for i, duration := range remaining {
		expiry := now.Add(duration).Format(time.RFC3339Nano)
		credits.Credits = append(credits.Credits, OpenAIRateLimitResetCreditDetail{ExpiresAt: expiry})
		candidates = append(candidates, openAIAutoResetCreditCandidate{ID: string(rune('A' + i)), ExpiresAt: expiry})
	}
	repo := &autoResetTestAccountRepo{account: &Account{
		ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditExpiryEnabledExtraKey: true,
			OpenAIAutoResetCreditStateExtraKey: &OpenAIAutoResetCreditState{
				Status: OpenAIAutoResetStatusAvailable, ExpiryInitialized: true, AvailableCount: len(remaining),
			},
			openaiQuotaResetCreditsKey: credits,
			"codex_5h_used_percent":    100.0,
			"codex_usage_updated_at":   now.Format(time.RFC3339Nano),
			"codex_5h_reset_at":        now.Add(time.Hour).Format(time.RFC3339Nano),
		},
	}}
	quota := &autoResetTestQuota{repo: repo, usage: &OpenAIQuotaUsage{
		FetchedAt: now.Unix(), autoResetDetailsComplete: true, autoResetCandidates: candidates,
		RateLimitResetCredits: credits,
		RateLimit: &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{
			UsedPercent: 100, ResetAt: now.Add(time.Hour).Unix(), ResetAfterSeconds: 3600, LimitWindowSeconds: 5 * 3600,
		}},
	}}
	config := DefaultIdempotencyConfig()
	config.ObserveOnly = false
	config.FailedRetryBackoff = 0
	svc := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{},
		NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), config), nil, nil, nil)
	svc.now = func() time.Time { return now }
	return svc, repo, quota, now
}

func TestOpenAIAutoResetExpiryConfigStrictAndIndependent(t *testing.T) {
	parentID := int64(1)
	for _, value := range []any{nil, "true", "false", 1, 0, 1.0} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
			OpenAIAutoResetCreditExpiryEnabledExtraKey: value,
		}}
		require.False(t, ResolveOpenAIAutoResetCreditConfig(account).ExpiryEnabled)
		_, err := normalizeOpenAIAutoResetCreditExtra(account.Platform, account.Type, false, account.Extra)
		require.Error(t, err)
	}
	for _, enabled := range []bool{false, true} {
		for _, expiry := range []bool{false, true} {
			extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{
				OpenAIAutoResetCreditEnabledExtraKey:       enabled,
				OpenAIAutoResetCreditExpiryEnabledExtraKey: expiry,
			})
			require.NoError(t, err)
			config := ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra})
			require.Equal(t, enabled, config.Enabled)
			require.Equal(t, expiry, config.ExpiryEnabled)
			if !enabled {
				require.NotContains(t, extra, OpenAIAutoResetCredit5hThresholdExtraKey)
				require.NotContains(t, extra, OpenAIAutoResetCredit7dThresholdExtraKey)
			}
		}
	}
	for _, account := range []*Account{
		{Platform: PlatformAnthropic, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID, QuotaDimension: QuotaDimensionSpark},
	} {
		account.Extra = map[string]any{OpenAIAutoResetCreditExpiryEnabledExtraKey: true}
		require.False(t, ResolveOpenAIAutoResetCreditConfig(account).ExpiryEnabled)
		_, err := normalizeOpenAIAutoResetCreditExtra(account.Platform, account.Type, account.IsShadow(), account.Extra)
		require.Error(t, err)
	}
	require.False(t, ResolveOpenAIAutoResetCreditConfig(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}).ExpiryEnabled)
}

func TestOpenAIAutoResetExpiryOnlyDoesNotChangeAutoPause(t *testing.T) {
	now := time.Now().UTC()
	for _, percent := range []float64{90, 100} {
		account := &Account{ID: 700, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
			"auto_pause_5h_threshold": 0.8, "auto_pause_7d_disabled": true,
			"codex_5h_used_percent": percent, "codex_usage_updated_at": now.Format(time.RFC3339),
			"codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
		}}
		wantPaused, wantDecision := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		account.Extra[OpenAIAutoResetCreditExpiryEnabledExtraKey] = true
		account.Extra[OpenAIAutoResetCreditStateExtraKey] = &OpenAIAutoResetCreditState{
			Status: OpenAIAutoResetStatusAvailable, AvailableCount: 1, CheckedAt: now.Format(time.RFC3339),
		}
		gotPaused, gotDecision := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.Equal(t, wantPaused, gotPaused)
		require.Equal(t, wantDecision, gotDecision)
	}
}

func TestOpenAIAutoResetFourPolicyCombinations(t *testing.T) {
	for _, threshold := range []bool{false, true} {
		for _, expiry := range []bool{false, true} {
			t.Run(stringBool(threshold)+"-"+stringBool(expiry), func(t *testing.T) {
				svc, repo, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
				repo.setExtraForTest(OpenAIAutoResetCreditEnabledExtraKey, threshold)
				repo.setExtraForTest(OpenAIAutoResetCreditExpiryEnabledExtraKey, expiry)
				require.NoError(t, svc.evaluateAccount(context.Background(), 700))
				if threshold || expiry {
					require.Equal(t, int32(1), quota.resetCalls.Load())
					if expiry {
						require.Equal(t, "expiry", repo.stateForTest().TriggerWindow)
					}
				} else {
					require.Zero(t, quota.queryCalls.Load())
					require.Zero(t, quota.resetCalls.Load())
				}
			})
		}
	}
}

func stringBool(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func TestOpenAIAutoResetExpiryScanBoundariesAndNanoTime(t *testing.T) {
	for _, remaining := range []time.Duration{61 * time.Minute, 40 * time.Minute, 10*time.Minute + time.Nanosecond, 10 * time.Minute, 5 * time.Minute, time.Nanosecond, 0, -time.Minute} {
		t.Run(remaining.String(), func(t *testing.T) {
			svc, repo, quota, now := newExpiryAutoResetFixture(t, remaining)
			// RFC3339Nano parsing must compare instants, not text or whole seconds.
			expiry := now.Add(remaining).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339Nano)
			quota.usage.RateLimitResetCredits.Credits[0].ExpiresAt = expiry
			quota.usage.autoResetCandidates[0].ExpiresAt = expiry
			due := remaining > 0 && remaining <= 10*time.Minute
			for i := 0; i < 3; i++ {
				svc.scanEnabledAccounts(context.Background())
			}
			if due {
				require.Len(t, svc.queue, 1)
				require.NoError(t, svc.evaluateAccount(context.Background(), <-svc.queue))
				require.Equal(t, int32(1), quota.resetCalls.Load())
				require.Equal(t, "A", quota.resetArgs[0][0])
			} else {
				require.Empty(t, svc.queue)
				require.Zero(t, repo.getCalls.Load())
				require.Zero(t, repo.extraWrites.Load())
				require.Zero(t, quota.queryCalls.Load())
			}
		})
	}
}

func TestOpenAIAutoResetExpiryInitializesOnceThenUsesPersistedSnapshot(t *testing.T) {
	for _, remaining := range [][]time.Duration{{61 * time.Minute}, {}} {
		svc, repo, quota, now := newExpiryAutoResetFixture(t, remaining...)
		repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, nil)
		require.NoError(t, svc.evaluateAccount(context.Background(), 700))
		require.Equal(t, int32(1), quota.queryCalls.Load())
		require.True(t, repo.stateForTest().ExpiryInitialized)
		for _, elapsed := range []time.Duration{time.Minute, 21 * time.Minute, 40 * time.Minute} {
			svc.now = func() time.Time { return now.Add(elapsed) }
			svc.scanEnabledAccounts(context.Background())
			require.Empty(t, svc.queue)
			require.NoError(t, svc.evaluateAccount(context.Background(), 700))
		}
		require.Equal(t, int32(1), quota.queryCalls.Load())
		restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
		restarted.now = func() time.Time { return now.Add(45 * time.Minute) }
		restarted.scanEnabledAccounts(context.Background())
		require.Empty(t, restarted.queue)
		require.Equal(t, int32(1), quota.queryCalls.Load())
		if len(remaining) > 0 {
			restarted.now = func() time.Time { return now.Add(51 * time.Minute) }
			restarted.scanEnabledAccounts(context.Background())
			require.Len(t, restarted.queue, 1)
			require.NoError(t, restarted.evaluateAccount(context.Background(), <-restarted.queue))
			require.Equal(t, int32(1), quota.resetCalls.Load())
		} else {
			restarted.now = func() time.Time { return now.Add(7 * 24 * time.Hour) }
			restarted.scanEnabledAccounts(context.Background())
			require.Empty(t, restarted.queue)
			require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
			require.Equal(t, int32(1), quota.queryCalls.Load())
		}
	}
}

func TestOpenAIAutoResetExpiryInitializationFailureBacksOff(t *testing.T) {
	svc, repo, quota, now := newExpiryAutoResetFixture(t, time.Hour)
	repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, nil)
	quota.queryErr = errors.New("offline")
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	}
	require.Equal(t, int32(1), quota.queryCalls.Load())
	require.False(t, repo.stateForTest().ExpiryInitialized)
	svc.now = func() time.Time { return now.Add(time.Minute) }
	quota.queryErr = nil
	require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, int32(2), quota.queryCalls.Load())
	require.True(t, repo.stateForTest().ExpiryInitialized)
}

func TestOpenAIAutoResetExpiryInvalidCachedSnapshotsFailClosed(t *testing.T) {
	for _, snapshot := range []any{nil, "bad", map[string]any{}, map[string]any{"available_count": -1},
		&OpenAIRateLimitResetCredits{AvailableCount: 1},
		&OpenAIRateLimitResetCredits{AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: "not-a-time"}}},
	} {
		svc, repo, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
		repo.setExtraForTest(openaiQuotaResetCreditsKey, snapshot)
		svc.scanEnabledAccounts(context.Background())
		require.Empty(t, svc.queue)
		require.NoError(t, svc.evaluateAccount(context.Background(), 700))
		require.Zero(t, quota.queryCalls.Load())
		require.Zero(t, quota.resetCalls.Load())
	}
}

func TestOpenAIAutoResetExpiryReassessesFreshEarliest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []openAIAutoResetCreditCandidate
		wantID     string
		wantError  bool
	}{
		{name: "fresh earliest changes", candidates: []openAIAutoResetCreditCandidate{{ID: "B", ExpiresAt: "2026-11-06T04:04:00Z"}, {ID: "A", ExpiresAt: "2026-11-06T04:08:00Z"}}, wantID: "B"},
		{name: "fresh earliest no longer near", candidates: []openAIAutoResetCreditCandidate{{ID: "A", ExpiresAt: "2026-11-06T04:40:00Z"}}},
		{name: "earliest missing id", candidates: []openAIAutoResetCreditCandidate{{ExpiresAt: "2026-11-06T04:02:00Z"}, {ID: "B", ExpiresAt: "2026-11-06T04:04:00Z"}}, wantError: true},
		{name: "unknown expiry anywhere", candidates: []openAIAutoResetCreditCandidate{{ID: "A", ExpiresAt: "2026-11-06T04:02:00Z"}, {ID: "B"}}, wantError: true},
		{name: "duplicate conflicting ids", candidates: []openAIAutoResetCreditCandidate{{ID: "A", ExpiresAt: "2026-11-06T04:02:00Z"}, {ID: "A", ExpiresAt: "2026-11-06T04:04:00Z"}}, wantError: true},
		{name: "fixed tie order", candidates: []openAIAutoResetCreditCandidate{{ID: "B", ExpiresAt: "2026-11-06T04:02:00Z"}, {ID: "A", ExpiresAt: "2026-11-06T04:02:00Z"}}, wantID: "A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute)
			quota.usage.autoResetCandidates = tc.candidates
			quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{AvailableCount: len(tc.candidates)}
			for _, candidate := range tc.candidates {
				quota.usage.RateLimitResetCredits.Credits = append(quota.usage.RateLimitResetCredits.Credits, OpenAIRateLimitResetCreditDetail{ExpiresAt: candidate.ExpiresAt})
			}
			err := svc.evaluateAccount(context.Background(), 700)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.wantID == "" {
				require.Zero(t, quota.resetCalls.Load())
				if len(tc.candidates) > 0 && tc.candidates[0].ID == "" {
					quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
					restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
					restarted.now = func() time.Time { return now }
					require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
					require.Equal(t, int32(1), quota.queryCalls.Load(), "hold the rejected earliest card without repeated queries")
					require.Zero(t, quota.resetCalls.Load())
				}
			} else {
				require.Equal(t, int32(1), quota.resetCalls.Load())
				require.Equal(t, tc.wantID, quota.resetArgs[0][0])
			}
		})
	}
}

func TestOpenAIAutoResetExpiryTimeoutNeverSwitchesAcrossCycleOrRestart(t *testing.T) {
	for _, resetErr := range []error{context.DeadlineExceeded, errors.New("upstream reset failed")} {
		svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
		quota.resetErr = resetErr
		require.Error(t, svc.evaluateAccount(context.Background(), 700))
		firstArgs := quota.resetArgs[0]
		require.Equal(t, "A", firstArgs[0])
		savedA := quota.usage.autoResetCandidates[0]
		quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
		quota.usage.RateLimit.PrimaryWindow.UsedPercent = 0
		quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
		quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}}}
		restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
		restarted.now = func() time.Time { return now.Add(time.Minute) }
		err := restarted.evaluateAccount(context.Background(), 700)
		require.Equal(t, "OPENAI_AUTO_RESET_ORIGINAL_CREDIT_UNAVAILABLE", infraerrors.Reason(err))
		require.Equal(t, int32(1), quota.resetCalls.Load(), "B must not be consumed while A is uncertain")
		quota.usage.autoResetCandidates = append(quota.usage.autoResetCandidates, savedA)
		quota.usage.RateLimitResetCredits.AvailableCount = 2
		quota.usage.RateLimitResetCredits.Credits = append(quota.usage.RateLimitResetCredits.Credits, OpenAIRateLimitResetCreditDetail{ExpiresAt: savedA.ExpiresAt})
		quota.resetErr = nil
		require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
		require.Len(t, quota.resetArgs, 2)
		require.Equal(t, firstArgs, quota.resetArgs[1])
	}
}

func TestOpenAIAutoResetExpiryNaturalExpirationUnblocksNextCredit(t *testing.T) {
	svc, _, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
	quota.failFirst = true
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	svc.now = func() time.Time { return now.Add(6 * time.Minute) }
	require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	require.Len(t, quota.resetArgs, 2)
	require.Equal(t, "A", quota.resetArgs[0][0])
	require.Equal(t, "B", quota.resetArgs[1][0])
}

func TestOpenAIAutoResetLegacyAttemptRetainsCycleIdentity(t *testing.T) {
	svc, repo, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
	state := repo.stateForTest()
	state.Status = OpenAIAutoResetStatusFailed
	state.TriggerWindow = "expiry"
	state.AttemptCreditHash = shortOpenAIAutoResetHash("A")
	state.AttemptCycleHash = "original-cycle-before-upgrade"
	repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, state)
	expected := uuid.NewSHA1(uuid.NameSpaceURL, []byte(openAIAutoResetStableKey(700, state))).String()
	require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, [2]string{"A", expected}, quota.resetArgs[0])
	require.Equal(t, state.AttemptCycleHash, repo.stateForTest().AttemptCycleHash)
}

func TestOpenAIAutoResetExpiryPostConsumeFailuresOnlyRepairLocalState(t *testing.T) {
	for _, failure := range []string{"cache", "state"} {
		t.Run(failure, func(t *testing.T) {
			svc, repo, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
			if failure == "cache" {
				quota.postCacheErr = errors.New("cache write failed")
			} else {
				repo.updateErr = func(updates map[string]any) error {
					if state, ok := updates[OpenAIAutoResetCreditStateExtraKey].(*OpenAIAutoResetCreditState); ok && state.AttemptConsumed {
						return errors.New("state write failed")
					}
					return nil
				}
			}
			require.Error(t, svc.evaluateAccount(context.Background(), 700))
			quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
			quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}}}
			quota.postCacheErr = nil
			repo.updateErr = nil
			require.NoError(t, svc.evaluateAccount(context.Background(), 700))
			require.Equal(t, int32(1), quota.resetCalls.Load())
			require.True(t, repo.stateForTest().AttemptConsumed)
			require.Equal(t, OpenAIAutoResetStatusSuccess, repo.stateForTest().Status)
		})
	}
}

func TestOpenAIAutoResetExpiryUnconfirmedResponseCannotSwitchCredit(t *testing.T) {
	svc, _, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
	quota.resetResult = &OpenAIQuotaResetResult{Code: "ok", Credit: &OpenAIQuotaResetCredit{ID: "B"}}
	err := svc.evaluateAccount(context.Background(), 700)
	require.Equal(t, "OPENAI_AUTO_RESET_TARGET_UNCONFIRMED", infraerrors.Reason(err))
	quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
	quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}}}
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, int32(1), quota.resetCalls.Load())
}

func TestOpenAIAutoResetExpiryRechecksBeforeConsume(t *testing.T) {
	for _, change := range []string{"switch", "expiry switch with threshold policy below threshold", "status", "schedulable", "deleted"} {
		t.Run(change, func(t *testing.T) {
			svc, repo, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
			if change == "expiry switch with threshold policy below threshold" {
				repo.setExtraForTest(OpenAIAutoResetCreditEnabledExtraKey, true)
				quota.usage.RateLimit.PrimaryWindow.UsedPercent = 0
			}
			repo.beforeGet = func(call int32) {
				if call != 3 {
					return
				}
				repo.mu.Lock()
				defer repo.mu.Unlock()
				switch change {
				case "switch", "expiry switch with threshold policy below threshold":
					repo.account.Extra[OpenAIAutoResetCreditExpiryEnabledExtraKey] = false
				case "status":
					repo.account.Status = StatusDisabled
				case "schedulable":
					repo.account.Schedulable = false
				case "deleted":
					repo.account = nil
				}
			}
			require.Error(t, svc.evaluateAccount(context.Background(), 700))
			require.Zero(t, quota.resetCalls.Load())
		})
	}
}

type unavailableAutoResetLock struct {
	IdempotencyRepository
}

func (unavailableAutoResetLock) TryLockOpenAIAutoResetCredit(context.Context, int64) (func(), bool, error) {
	return nil, false, errors.New("database lock unavailable")
}

func TestOpenAIAutoResetExpiryDatabaseRequired(t *testing.T) {
	for _, repository := range []IdempotencyRepository{nil, unavailableAutoResetLock{}} {
		svc, _, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
		svc.idempotency.repo = repository
		require.Error(t, svc.evaluateAccount(context.Background(), 700))
		require.Zero(t, quota.resetCalls.Load())
	}
}

func TestOpenAIAutoResetExpiryConcurrentInstancesShareBothPolicies(t *testing.T) {
	svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
	repo.setExtraForTest(OpenAIAutoResetCreditEnabledExtraKey, true)
	quota.resetEntered = make(chan struct{})
	quota.releaseReset = make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- svc.evaluateAccount(context.Background(), 700) }()
	<-quota.resetEntered
	other := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{},
		NewIdempotencyCoordinator(svc.idempotency.repo, svc.idempotency.cfg), nil, nil, nil)
	other.now = func() time.Time { return now }
	require.NoError(t, other.evaluateAccount(context.Background(), 700))
	close(quota.releaseReset)
	require.NoError(t, <-result)
	quota.releaseReset = nil
	quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
	for i := 0; i < 3; i++ {
		require.NoError(t, other.evaluateAccount(context.Background(), 700))
	}
	require.Equal(t, int32(1), quota.resetCalls.Load())
}

type unavailableAutoResetStore struct {
	*inMemoryIdempotencyRepo
}

func (unavailableAutoResetStore) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return false, errors.New("idempotency write unavailable")
}

func TestOpenAIAutoResetExpiryIdempotencyWriteRequired(t *testing.T) {
	svc, _, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute)
	svc.idempotency.repo = unavailableAutoResetStore{newInMemoryIdempotencyRepo()}
	err := svc.evaluateAccount(context.Background(), 700)
	require.Equal(t, "IDEMPOTENCY_STORE_UNAVAILABLE", infraerrors.Reason(err))
	require.Zero(t, quota.resetCalls.Load())
}

func TestOpenAIAutoResetExpiryIncompleteFreshDetailsCannotConsume(t *testing.T) {
	svc, _, quota, _ := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
	quota.usage.autoResetDetailsComplete = false
	err := svc.evaluateAccount(context.Background(), 700)
	require.Equal(t, "RESET_CREDIT_DETAILS_UNAVAILABLE", infraerrors.Reason(err))
	require.Zero(t, quota.resetCalls.Load())
}

func TestOpenAIAutoResetExpiryReenableAfterSuccessInitializesOnlyOnce(t *testing.T) {
	svc, repo, quota, now := newExpiryAutoResetFixture(t, 40*time.Minute)
	state := repo.stateForTest()
	state.Status = OpenAIAutoResetStatusSuccess
	state.ExpiryInitialized = false
	state.AttemptCreditHash = shortOpenAIAutoResetHash("previously-consumed-credit")
	state.AttemptConsumed = true
	repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, state)
	require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, int32(1), quota.queryCalls.Load())
	for i := 1; i < 4; i++ {
		svc.now = func() time.Time { return now.Add(time.Duration(i) * time.Minute) }
		svc.scanEnabledAccounts(context.Background())
		require.Empty(t, svc.queue)
		require.NoError(t, svc.evaluateAccount(context.Background(), 700))
	}
	require.Equal(t, int32(1), quota.queryCalls.Load())
	require.Zero(t, quota.resetCalls.Load())
}

func TestOpenAIAutoResetExpiryRestartsInterruptedOriginalRequest(t *testing.T) {
	for _, scenario := range []string{"expired lease", "active lease", "original disappeared", "legacy success"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
			state := repo.stateForTest()
			state.Status = OpenAIAutoResetStatusResetting
			state.TriggerWindow = "expiry"
			state.AttemptCreditHash = shortOpenAIAutoResetHash("A")
			state.AttemptCycleHash = "old-cycle"
			repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, state)
			key := openAIAutoResetStableKey(700, state)
			fingerprint, err := BuildIdempotencyFingerprint(http.MethodPost, "/system/openai/reset-credit/auto", "account:700", map[string]any{
				"account_id": int64(700), "credit_hash": state.AttemptCreditHash, "cycle_hash": state.AttemptCycleHash,
			})
			require.NoError(t, err)
			lease := time.Now().Add(-time.Minute)
			if scenario == "active lease" {
				lease = time.Now().Add(time.Hour)
			}
			record := &IdempotencyRecord{
				Scope: "openai_auto_reset_credit", IdempotencyKeyHash: HashIdempotencyKey(key),
				RequestFingerprint: fingerprint, Status: IdempotencyStatusProcessing,
				LockedUntil: &lease, ExpiresAt: time.Now().Add(openAIAutoResetAttemptTTL),
			}
			created, err := svc.idempotency.repo.CreateProcessing(context.Background(), record)
			require.NoError(t, err)
			require.True(t, created)
			if scenario == "legacy success" {
				require.NoError(t, svc.idempotency.repo.MarkSucceeded(context.Background(), record.ID, 200, `{"code":"ok","windows_reset":2}`, record.ExpiresAt))
			}
			if scenario == "original disappeared" {
				quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
				quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
					AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
				}
			}
			quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
			restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
			restarted.now = func() time.Time { return now }
			err = restarted.evaluateAccount(context.Background(), 700)
			if scenario == "original disappeared" || scenario == "legacy success" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if scenario == "expired lease" {
				require.Len(t, quota.resetArgs, 1)
				require.Equal(t, [2]string{"A", uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String()}, quota.resetArgs[0])
			} else {
				require.Zero(t, quota.resetCalls.Load())
			}
			if scenario == "legacy success" {
				require.Equal(t, quota.usage.autoResetCandidates[0].ExpiresAt, repo.stateForTest().AttemptExpiresAt)
				restarted.now = func() time.Time { return now.Add(6 * time.Minute) }
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Len(t, quota.resetArgs, 1)
				require.Equal(t, "B", quota.resetArgs[0][0])
			}
		})
	}
}

func TestOpenAIAutoResetExpiryMalformedDetailsCannotClearOriginalAttempt(t *testing.T) {
	svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
	quota.failFirst = true
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	originalExpiry := repo.stateForTest().AttemptExpiresAt
	svc.now = func() time.Time { return now.Add(time.Minute) }
	quota.usage.autoResetCandidates = []openAIAutoResetCreditCandidate{
		{ID: "A", ExpiresAt: now.Add(-time.Minute).Format(time.RFC3339Nano)},
		{ID: "A", ExpiresAt: originalExpiry},
		{ID: "B", ExpiresAt: now.Add(8 * time.Minute).Format(time.RFC3339Nano)},
	}
	quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{AvailableCount: 3}
	for _, candidate := range quota.usage.autoResetCandidates {
		quota.usage.RateLimitResetCredits.Credits = append(quota.usage.RateLimitResetCredits.Credits, OpenAIRateLimitResetCreditDetail{ExpiresAt: candidate.ExpiresAt})
	}
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, shortOpenAIAutoResetHash("A"), repo.stateForTest().AttemptCreditHash)
	require.Equal(t, originalExpiry, repo.stateForTest().AttemptExpiresAt)
	quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[2:]
	quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
		AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
	}
	require.Error(t, svc.evaluateAccount(context.Background(), 700))
	require.Equal(t, int32(1), quota.resetCalls.Load())
}

func TestOpenAIAutoResetExpiryRejectedEarliestSurvivesRestart(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		for _, tied := range []bool{false, true} {
			t.Run("incomplete-"+stringBool(incomplete)+"/tied-"+stringBool(tied), func(t *testing.T) {
				later := 8 * time.Minute
				if tied {
					later = 5 * time.Minute
				}
				svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, later)
				if incomplete {
					quota.usage.autoResetDetailsComplete = false
				} else {
					quota.usage.autoResetCandidates[0].ID = ""
				}
				require.Error(t, svc.evaluateAccount(context.Background(), 700))
				require.Zero(t, quota.resetCalls.Load())
				require.Equal(t, now.Add(5*time.Minute).Format(time.RFC3339Nano), repo.stateForTest().UnresolvedEarliestUntil)
				writes := repo.extraWrites.Load()
				quota.usage.autoResetDetailsComplete = true
				quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
				quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
					AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
				}
				quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
				restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
				for minute := 1; minute < 5; minute++ {
					restarted.now = func() time.Time { return now.Add(time.Duration(minute) * time.Minute) }
					restarted.scanEnabledAccounts(context.Background())
					require.Empty(t, restarted.queue)
					require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
					require.Zero(t, quota.resetCalls.Load(), "A is unresolved and has not naturally expired")
					require.Equal(t, int32(1), quota.queryCalls.Load())
					require.Equal(t, writes, repo.extraWrites.Load())
				}
				repo.setExtraForTest(OpenAIAutoResetCreditEnabledExtraKey, true)
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, int32(1), quota.queryCalls.Load(), "the threshold policy cannot bypass the same barrier")
				require.Equal(t, writes, repo.extraWrites.Load())
				restarted.now = func() time.Time { return now.Add(5 * time.Minute) }
				quota.usage.autoResetDetailsComplete = false
				require.Error(t, restarted.evaluateAccount(context.Background(), 700))
				require.Zero(t, quota.resetCalls.Load(), "an incomplete list cannot release the expired barrier")
				quota.usage.autoResetDetailsComplete = true
				restarted.now = func() time.Time { return now.Add(6 * time.Minute) }
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				if tied {
					require.Zero(t, quota.resetCalls.Load(), "both tied credits have expired")
				} else {
					require.Len(t, quota.resetArgs, 1)
					require.Equal(t, "B", quota.resetArgs[0][0])
				}
			})
		}
	}
}

func TestOpenAIAutoResetExpiryUnknownEarliestRemainsBlocked(t *testing.T) {
	for _, missing := range []string{"details", "expiration", "count"} {
		t.Run(missing, func(t *testing.T) {
			svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
			repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, nil)
			repo.setExtraForTest(openaiQuotaResetCreditsKey, nil)
			switch missing {
			case "details":
				quota.usage.RateLimitResetCredits = nil
				quota.usage.autoResetCandidates = nil
			case "expiration":
				quota.usage.autoResetCandidates[0] = openAIAutoResetCreditCandidate{}
				quota.usage.RateLimitResetCredits.Credits[0].ExpiresAt = ""
			case "count":
				quota.usage.autoResetDetailsComplete = false
				quota.usage.autoResetCandidates = nil
			}
			require.Error(t, svc.evaluateAccount(context.Background(), 700))
			require.Equal(t, "unknown", repo.stateForTest().UnresolvedEarliestUntil)
			writes := repo.extraWrites.Load()
			quota.usage.autoResetDetailsComplete = true
			quota.usage.autoResetCandidates = []openAIAutoResetCreditCandidate{{
				ID: "B", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
			}}
			quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
				AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
			}
			repo.setExtraForTest(openaiQuotaResetCreditsKey, quota.usage.RateLimitResetCredits)
			quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
			restarted := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, svc.idempotency, nil, nil, nil)
			for _, elapsed := range []time.Duration{time.Minute, 55 * time.Minute, 2 * time.Hour} {
				restarted.now = func() time.Time { return now.Add(elapsed) }
				restarted.scanEnabledAccounts(context.Background())
				require.Empty(t, restarted.queue)
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Zero(t, quota.resetCalls.Load())
				require.Equal(t, int32(1), quota.queryCalls.Load(), "unknown original expiration cannot be replaced by a later list")
				require.Equal(t, writes, repo.extraWrites.Load())
			}
		})
	}
}

type autoResetFailSucceededRepo struct {
	*inMemoryIdempotencyRepo
	fail bool
}

func (r *autoResetFailSucceededRepo) MarkSucceeded(ctx context.Context, id int64, status int, body string, expiry time.Time) error {
	if r.fail {
		return errors.New("idempotency success write unavailable")
	}
	return r.inMemoryIdempotencyRepo.MarkSucceeded(ctx, id, status, body, expiry)
}

func TestOpenAIAutoResetExpiryConfirmedConsumeSurvivesIdempotencyFailure(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, originalPresent := range []bool{false, true} {
			for _, legacy := range []bool{false, true} {
				t.Run("restart-"+stringBool(restart)+"/original-"+stringBool(originalPresent)+"/legacy-"+stringBool(legacy), func(t *testing.T) {
					svc, repo, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
					if legacy {
						state := repo.stateForTest()
						state.AttemptCreditHash = shortOpenAIAutoResetHash("A")
						state.AttemptCycleHash = "started-before-upgrade"
						state.TriggerWindow = "expiry"
						repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, state)
					}
					store := &autoResetFailSucceededRepo{inMemoryIdempotencyRepo: newInMemoryIdempotencyRepo(), fail: true}
					svc.idempotency.repo = store
					require.Error(t, svc.evaluateAccount(context.Background(), 700))
					require.Equal(t, int32(1), quota.resetCalls.Load())
					require.True(t, repo.stateForTest().AttemptConsumed, "checkpoint the validated upstream success before MarkSucceeded")
					require.Equal(t, "IDEMPOTENCY_STORE_UNAVAILABLE", repo.stateForTest().ErrorCode)
					state := repo.stateForTest()
					state.UnresolvedEarliestUntil = "unknown"
					repo.setExtraForTest(OpenAIAutoResetCreditStateExtraKey, state)
					store.mu.Lock()
					for _, record := range store.data {
						if record.Status == IdempotencyStatusProcessing {
							expired := time.Now().Add(-time.Minute)
							record.LockedUntil = &expired
						}
					}
					store.mu.Unlock()
					store.fail = false
					if !originalPresent {
						quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
						quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
							AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
						}
					}
					quota.usage.RateLimit.PrimaryWindow.ResetAt += 5 * 3600
					if restart {
						svc = NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{},
							NewIdempotencyCoordinator(store, svc.idempotency.cfg), nil, nil, nil)
					}
					svc.now = func() time.Time { return now.Add(time.Minute) }
					quota.postCacheErr = errors.New("local snapshot unavailable")
					require.Error(t, svc.evaluateAccount(context.Background(), 700))
					require.Equal(t, int32(1), quota.resetCalls.Load())
					quota.postCacheErr = nil
					require.NoError(t, svc.evaluateAccount(context.Background(), 700))
					require.Equal(t, int32(1), quota.resetCalls.Load(), "only local recovery may run after confirmed consumption")
					require.True(t, repo.stateForTest().AttemptConsumed)
					require.Equal(t, OpenAIAutoResetStatusSuccess, repo.stateForTest().Status)
					if legacy {
						require.Equal(t, "started-before-upgrade", repo.stateForTest().AttemptCycleHash)
					}
				})
			}
		}
	}
}

func TestOpenAIAutoResetExpiryMissingConsumeFieldsCannotPromoteLaterCard(t *testing.T) {
	for _, response := range []*OpenAIQuotaResetResult{
		{}, {Code: "ok"}, {Code: "ok", Credit: &OpenAIQuotaResetCredit{}},
		{Credit: &OpenAIQuotaResetCredit{ID: "A"}},
	} {
		svc, _, quota, now := newExpiryAutoResetFixture(t, 5*time.Minute, 8*time.Minute)
		quota.resetResult = response
		err := svc.evaluateAccount(context.Background(), 700)
		require.Equal(t, "OPENAI_AUTO_RESET_TARGET_UNCONFIRMED", infraerrors.Reason(err))
		quota.usage.autoResetCandidates = quota.usage.autoResetCandidates[1:]
		quota.usage.RateLimitResetCredits = &OpenAIRateLimitResetCredits{
			AvailableCount: 1, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: quota.usage.autoResetCandidates[0].ExpiresAt}},
		}
		svc.now = func() time.Time { return now.Add(time.Minute) }
		require.Error(t, svc.evaluateAccount(context.Background(), 700))
		require.Equal(t, int32(1), quota.resetCalls.Load())
	}
}

func TestOpenAIAutoResetExpiryRealHTTPFailureCheckpoints(t *testing.T) {
	for _, mode := range []string{"missing-id", "incomplete", "missing-expiry", "missing-details", "mark-succeeded-A", "mark-succeeded-B", "response-credit", "response-code"} {
		t.Run(mode, func(t *testing.T) {
			svc, repo, _, _ := newExpiryAutoResetFixture(t)
			now := time.Now().UTC()
			svc.now = func() time.Time { return now }
			aExpiry := now.Add(5 * time.Minute).Format(time.RFC3339Nano)
			bExpiry := now.Add(8 * time.Minute).Format(time.RFC3339Nano)
			repo.setExtraForTest(openaiQuotaResetCreditsKey, &OpenAIRateLimitResetCredits{
				AvailableCount: 2, Credits: []OpenAIRateLimitResetCreditDetail{{ExpiresAt: aExpiry}, {ExpiresAt: bExpiry}},
			})
			repo.account.Credentials = map[string]any{"chatgpt_account_id": "isolated-test-account"}
			confirmed := mode == "mark-succeeded-A" || mode == "mark-succeeded-B"
			unconfirmed := mode == "response-credit" || mode == "response-code"
			store := &autoResetFailSucceededRepo{inMemoryIdempotencyRepo: newInMemoryIdempotencyRepo(), fail: confirmed}
			svc.idempotency.repo = store
			var phase, queries, consumes atomic.Int32
			var requested atomic.Value
			requested.Store(map[string]string{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("content-type", "application/json")
				p := phase.Load()
				count := 2
				if p > 0 && mode != "mark-succeeded-A" {
					count = 1
				}
				switch r.URL.Path {
				case "/backend-api/wham/usage":
					queries.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"rate_limit_reset_credits": map[string]any{"available_count": count},
						"rate_limit": map[string]any{"primary_window": map[string]any{
							"used_percent": 0, "limit_window_seconds": 5 * 3600,
							"reset_at": now.Add(time.Hour).Unix() + int64(p)*5*3600,
						}},
					})
				case "/backend-api/wham/rate-limit-reset-credits":
					if p == 0 && mode == "missing-details" {
						_, _ = w.Write([]byte(`{}`))
						return
					}
					credits := []map[string]any{}
					if count == 2 {
						credit := map[string]any{"id": "A", "expires_at": aExpiry}
						if p == 0 {
							switch mode {
							case "missing-id":
								delete(credit, "id")
							case "missing-expiry":
								delete(credit, "expires_at")
							case "incomplete":
								count++
							}
						}
						credits = append(credits, credit)
					}
					credits = append(credits, map[string]any{"id": "B", "expires_at": bExpiry})
					_ = json.NewEncoder(w).Encode(map[string]any{"available_count": count, "credits": credits})
				case "/backend-api/wham/rate-limit-reset-credits/consume":
					body := map[string]string{}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					consumes.Add(1)
					requested.Store(body)
					response := map[string]any{"code": "ok", "credit": map[string]any{"id": body["credit_id"]}}
					if p == 0 && unconfirmed {
						if mode == "response-credit" {
							delete(response, "credit")
						} else {
							delete(response, "code")
						}
					}
					_ = json.NewEncoder(w).Encode(response)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(repo.account): "fake-token"}}
			realQuota := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(server), nil)
			svc.quota = realQuota
			require.Error(t, svc.evaluateAccount(context.Background(), 700))
			if confirmed || unconfirmed {
				require.Equal(t, int32(1), consumes.Load())
				require.Equal(t, "A", requested.Load().(map[string]string)["credit_id"])
				require.NotEmpty(t, requested.Load().(map[string]string)["redeem_request_id"])
				require.Equal(t, confirmed, repo.stateForTest().AttemptConsumed)
			} else {
				require.Zero(t, consumes.Load())
				require.Equal(t, aExpiry, repo.stateForTest().UnresolvedEarliestUntil)
			}
			phase.Store(1)
			restarted := NewOpenAIQuotaAutoResetService(repo, realQuota, autoResetTestRecoverer{},
				NewIdempotencyCoordinator(store, svc.idempotency.cfg), nil, nil, nil)
			restarted.now = func() time.Time { return now.Add(time.Minute) }
			if confirmed {
				store.mu.Lock()
				for _, record := range store.data {
					if record.Status == IdempotencyStatusProcessing {
						expired := time.Now().Add(-time.Minute)
						record.LockedUntil = &expired
					}
				}
				store.mu.Unlock()
				store.fail = false
				repo.updateErr = func(updates map[string]any) error {
					if _, postReset := updates[openaiQuotaCreditsKey]; postReset {
						return errors.New("post-reset cache unavailable")
					}
					return nil
				}
				require.Error(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, int32(1), consumes.Load())
				repo.updateErr = nil
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, OpenAIAutoResetStatusSuccess, repo.stateForTest().Status)
				require.Equal(t, int32(1), consumes.Load(), "real HTTP consume must not be submitted during local repair")
			} else if unconfirmed {
				require.Error(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, int32(1), consumes.Load(), "missing response fields cannot confirm A or promote B")
			} else {
				writes := repo.extraWrites.Load()
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, int32(1), queries.Load())
				require.Equal(t, writes, repo.extraWrites.Load())
				require.Zero(t, consumes.Load())
				restarted.now = func() time.Time { return now.Add(6 * time.Minute) }
				require.NoError(t, restarted.evaluateAccount(context.Background(), 700))
				require.Equal(t, int32(1), consumes.Load())
				require.Equal(t, "B", requested.Load().(map[string]string)["credit_id"])
			}
		})
	}
}
