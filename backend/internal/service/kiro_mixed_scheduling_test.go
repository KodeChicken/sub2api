//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func kiroMixedFixture() (*Account, *Group) {
	a := kiroTestAccount()
	a.GroupIDs = []int64{10}
	a.AccountGroups = []AccountGroup{{GroupID: 10}}
	a.Extra = map[string]any{"mixed_scheduling": true}
	a.Credentials["model_mapping"] = map[string]any{
		"claude-sonnet-4.6": "claude-sonnet-4.6",
		"public-alias":      "claude-sonnet-4.6",
	}
	return a, &Group{ID: 10, Name: "Claude", Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
}

type kiroMixedRepo struct{ *mockAccountRepoForPlatform }

func (r *kiroMixedRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func kiroMixedGateway(a *Account, group *Group, loadAware bool) *GatewayService {
	repo := &kiroMixedRepo{&mockAccountRepoForPlatform{
		accounts: []Account{*a}, accountsByID: map[int64]*Account{a.ID: a},
	}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = loadAware
	return &GatewayService{
		accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{group.ID: group}},
		cache: &mockGatewayCacheForPlatform{}, cfg: cfg,
		concurrencyService: NewConcurrencyService(&mockConcurrencyCache{}),
	}
}

func TestKiroMixedFiniteModels(t *testing.T) {
	a, _ := kiroMixedFixture()
	for _, model := range []string{"public-alias", "claude-sonnet-4.6", "claude-sonnet-4-6-20260514"} {
		target, ok, err := a.resolveFiniteKiroModel(model)
		require.NoError(t, err)
		require.True(t, ok, model)
		require.Equal(t, "claude-sonnet-4.6", target)
		require.True(t, a.IsModelSupported(model))
		require.Equal(t, target, a.GetMappedModel(model))
	}
	for _, model := range []string{"claude-sonnet-4-7", "claude-sonnet-4-6-latest", "claude-sonnet-4-6-thinking", "CLAUDE-SONNET-4-6-LATEST"} {
		_, ok, err := a.resolveFiniteKiroModel(model)
		require.NoError(t, err)
		require.False(t, ok, model)
	}
	a.Credentials["model_mapping"] = map[string]any{
		"claude-sonnet-4.6": "claude-sonnet-4.6",
		"claude-sonnet-4-6": "claude-opus-4.6",
	}
	_, _, err := a.resolveFiniteKiroModel("claude-sonnet-4-6-20260514")
	require.ErrorContains(t, err, "ambiguous")
	require.False(t, a.IsModelSupported("claude-sonnet-4-6-20260514"))
	target, ok, err := a.resolveFiniteKiroModel("claude-sonnet-4-6")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "claude-opus-4.6", target, "an exact alias has priority")
	a.Credentials["model_mapping"] = map[string]any{"public-alias": "claude-sonnet-4-6-20260514"}
	require.Equal(t, "claude-sonnet-4.6", a.GetMappedModel("public-alias"), "channel restriction and outbound use the same actual target")

	other := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"model_mapping": map[string]any{"claude-*": "native"}}}
	require.True(t, other.IsModelSupported("claude-anything"))
	require.Equal(t, "native", other.GetMappedModel("claude-anything"))
}

func TestKiroMixedEligibilityAndProviderIsolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Account, *Group) context.Context
		want   bool
	}{
		{"explicit optin", func(*Account, *Group) context.Context { return context.Background() }, true},
		{"default off", func(a *Account, _ *Group) context.Context { a.Extra = nil; return context.Background() }, false},
		{"string is not bool", func(a *Account, _ *Group) context.Context {
			a.Extra["mixed_scheduling"] = "true"
			return context.Background()
		}, false},
		{"unknown membership", func(a *Account, _ *Group) context.Context {
			a.GroupIDs = nil
			a.AccountGroups = nil
			return context.Background()
		}, false},
		{"cross group", func(_ *Account, g *Group) context.Context { g.ID++; return context.Background() }, false},
		{"gemini", func(_ *Account, g *Group) context.Context { g.Platform = PlatformGemini; return context.Background() }, false},
		{"openai", func(_ *Account, g *Group) context.Context { g.Platform = PlatformOpenAI; return context.Background() }, false},
		{"composite anthropic target", func(_ *Account, g *Group) context.Context {
			g.Platform = PlatformComposite
			return WithResolvedTargetPlatform(context.Background(), PlatformAnthropic)
		}, false},
		{"forced anthropic", func(*Account, *Group) context.Context {
			return context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAnthropic)
		}, false},
		{"resolved other target", func(*Account, *Group) context.Context {
			return WithResolvedTargetPlatform(context.Background(), PlatformGemini)
		}, false},
		{"inactive group", func(_ *Account, g *Group) context.Context { g.Status = "disabled"; return context.Background() }, false},
		{"empty mapping", func(a *Account, _ *Group) context.Context {
			delete(a.Credentials, "model_mapping")
			return context.Background()
		}, false},
		{"only wildcard", func(a *Account, _ *Group) context.Context {
			a.Credentials["model_mapping"] = map[string]any{"claude-*": "claude-sonnet-4.6"}
			return context.Background()
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, g := kiroMixedFixture()
			ctx := tc.change(a, g)
			require.Equal(t, tc.want, kiroMixedRequestAllowed(ctx, a, g, "claude-sonnet-4-6-20260514"))
			require.False(t, a.IsMixedSchedulingEnabled(), "do not broaden Antigravity's Gemini helper")
		})
	}
	a, _ := kiroMixedFixture()
	require.False(t, kiroMixedRequestAllowed(context.Background(), a, nil, "public-alias"))
}

type kiroMixedSnapshotCache struct {
	SchedulerCache
	pool []*Account
	full map[int64]*Account
}

func (c *kiroMixedSnapshotCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	return c.pool, true, nil
}
func (c *kiroMixedSnapshotCache) GetAccount(_ context.Context, id int64) (*Account, error) {
	return c.full[id], nil
}

func TestKiroMixedSchedulersColdWarmStickyAndRouting(t *testing.T) {
	for _, load := range []bool{false, true} {
		for _, warm := range []bool{false, true} {
			for _, routed := range []bool{false, true} {
				t.Run(map[bool]string{false: "legacy", true: "load"}[load]+map[bool]string{false: "/cold", true: "/warm"}[warm]+map[bool]string{false: "/sticky", true: "/route"}[routed], func(t *testing.T) {
					a, g := kiroMixedFixture()
					if routed {
						g.ModelRoutingEnabled = true
						g.ModelRouting = map[string][]int64{"claude-sonnet-4-6-20260514": {a.ID}}
					}
					s := kiroMixedGateway(a, g, load)
					s.cache = &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"session": a.ID}}
					if warm {
						s.schedulerSnapshot = NewSchedulerSnapshotService(&kiroMixedSnapshotCache{
							pool: []*Account{a}, full: map[int64]*Account{a.ID: a},
						}, nil, s.accountRepo, s.groupRepo, s.cfg)
					}
					result, err := s.SelectAccountWithLoadAwareness(context.Background(), &g.ID, "session", "claude-sonnet-4-6-20260514", nil, "", 0)
					require.NoError(t, err)
					require.Equal(t, PlatformKiro, result.Account.Platform)
					require.Equal(t, a.ID, result.Account.ID)
					if result.ReleaseFunc != nil {
						result.ReleaseFunc()
					}
					a.Extra["mixed_scheduling"] = false
					result, err = s.SelectAccountWithLoadAwareness(context.Background(), &g.ID, "session", "claude-sonnet-4-6-20260514", nil, "", 0)
					require.ErrorIs(t, err, ErrNoAvailableAccounts)
					require.Nil(t, result, "sticky and route cannot bypass opt-out")
				})
			}
		}
	}
}

func TestKiroMixedSimpleGlobalPoolAndForcedTargets(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		edit func(*Account, *Group)
	}{
		{"cross-group global pool", context.Background(), func(a *Account, _ *Group) { a.GroupIDs = []int64{99}; a.AccountGroups = []AccountGroup{{GroupID: 99}} }},
		{"ungrouped", context.Background(), func(a *Account, _ *Group) { a.GroupIDs = nil; a.AccountGroups = nil }},
		{"forced anthropic", context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAnthropic), func(*Account, *Group) {}},
		{"forced antigravity", context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAntigravity), func(*Account, *Group) {}},
		{"gemini", context.Background(), func(_ *Account, g *Group) { g.Platform = PlatformGemini }},
		{"openai", context.Background(), func(_ *Account, g *Group) { g.Platform = PlatformOpenAI }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, g := kiroMixedFixture()
			tc.edit(a, g)
			s := kiroMixedGateway(a, g, true)
			s.cfg.RunMode = config.RunModeSimple
			s.schedulerSnapshot = NewSchedulerSnapshotService(&kiroMixedSnapshotCache{
				pool: []*Account{a}, full: map[int64]*Account{a.ID: a},
			}, nil, s.accountRepo, s.groupRepo, s.cfg)
			selection, err := s.SelectAccountWithLoadAwareness(tc.ctx, &g.ID, "", "public-alias", nil, "", 0)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection)
		})
	}
}

func TestKiroMixedUnknownModelAndExcludedAccountFallBackToNative(t *testing.T) {
	for _, load := range []bool{false, true} {
		a, g := kiroMixedFixture()
		s := kiroMixedGateway(a, g, load)
		native := Account{ID: 90, Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 10,
			GroupIDs: []int64{g.ID}, AccountGroups: []AccountGroup{{GroupID: g.ID}}}
		repo := s.accountRepo.(*kiroMixedRepo)
		repo.accounts = append(repo.accounts, native)
		repo.accountsByID[native.ID] = &native
		for _, tc := range []struct {
			model    string
			excluded map[int64]struct{}
		}{
			{"claude-opus-99", nil},
			{"public-alias", map[int64]struct{}{a.ID: {}}},
		} {
			selected, err := s.SelectAccountWithLoadAwareness(context.Background(), &g.ID, "", tc.model, tc.excluded, "", 0)
			require.NoError(t, err)
			require.Equal(t, native.ID, selected.Account.ID)
			selected.ReleaseFunc()
		}
	}
}

func TestKiroMixedDoesNotExpandTemporaryDispatch(t *testing.T) {
	a, g := kiroMixedFixture()
	a.GroupIDs, a.AccountGroups = nil, nil
	temporary := temporaryDispatchTestGroup(a.ID, time.Now().Add(time.Hour))
	temporary.ID = g.ID
	s := kiroMixedGateway(a, temporary, false)
	_, err := s.SelectAccountForModel(context.Background(), &g.ID, "", "public-alias")
	require.Error(t, err, "the existing exact-platform temporary policy remains intact")
}

func TestKiroMixedModelsCatalogCandidatesAndDiagnosis(t *testing.T) {
	a, g := kiroMixedFixture()
	s := kiroMixedGateway(a, g, false)
	admin := &adminServiceImpl{accountRepo: s.accountRepo, groupRepo: s.groupRepo}
	require.ElementsMatch(t, []string{"claude-sonnet-4.6", "public-alias"}, s.GetAvailableModels(context.Background(), &g.ID, PlatformAnthropic))
	candidates, err := admin.GetGroupModelsListCandidates(context.Background(), g.ID, "")
	require.NoError(t, err)
	require.Contains(t, candidates, "public-alias")
	require.Empty(t, defaultModelsListCandidateIDs(PlatformKiro), "no invented Kiro defaults")
	diag := s.DiagnoseModelAvailabilityForPlatform(context.Background(), &g.ID, "claude-sonnet-4-6-20260514", PlatformAnthropic)
	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport)
	diag = s.DiagnoseModelAvailabilityForPlatform(context.Background(), &g.ID, "claude-opus-99", PlatformAnthropic)
	require.True(t, diag.HasAccountsInPool)
	require.False(t, diag.HasModelSupport)

	a.Extra["mixed_scheduling"] = false
	s.accountRepo.(*kiroMixedRepo).accounts = []Account{*a}
	require.Empty(t, s.GetAvailableModels(context.Background(), &g.ID, PlatformAnthropic))
	candidates, err = admin.GetGroupModelsListCandidates(context.Background(), g.ID, "")
	require.NoError(t, err)
	require.NotContains(t, candidates, "public-alias")
	diag = s.DiagnoseModelAvailabilityForPlatform(context.Background(), &g.ID, "public-alias", PlatformAnthropic)
	require.False(t, diag.HasAccountsInPool)
}

func TestKiroMixedSelectedMessagesToolsAndCountTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[streaming], func(t *testing.T) {
			a, g := kiroMixedFixture()
			gateway := kiroMixedGateway(a, g, true)
			calls := 0
			transport := &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "Bearer ksk_example", req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("x-api-key"))
				require.Empty(t, req.Header.Get("anthropic-beta"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				require.NotContains(t, body, "messages")
				state := body["conversationState"].(map[string]any)
				current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
				require.Equal(t, "claude-sonnet-4.6", current["modelId"])
				encoded, err := json.Marshal(body)
				require.NoError(t, err)
				require.Contains(t, string(encoded), `"toolUseId":"previous"`)
				require.Contains(t, string(encoded), "toolResults")
				data := append(kiroTestFrame(t, "toolUseEvent", `{"toolUseId":"next","name":"read","input":"{\"path\":\"next.txt\"}","stop":true}`),
					kiroTestFrame(t, "metadataEvent", `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":7},"stopReason":"TOOL_USE"}`)...)
				return kiroTestResponse(200, string(data)), nil
			}}
			gateway.kiroService = NewKiroService(gateway.accountRepo, transport, NewOAuthRefreshAPI(gateway.accountRepo, nil))
			body, err := json.Marshal(map[string]any{
				"model": "claude-sonnet-4-6-20260514", "stream": streaming, "max_tokens": 256,
				"tools": []any{map[string]any{"name": "read", "description": "Read file", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}},
				"messages": []any{
					map[string]any{"role": "user", "content": "read"},
					map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "previous", "name": "read", "input": map[string]any{"path": "first.txt"}}}},
					map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "previous", "content": "file content"}}},
				},
			})
			require.NoError(t, err)
			selected, err := gateway.SelectAccountWithLoadAwareness(context.Background(), &g.ID, "", "claude-sonnet-4-6-20260514", nil, "", 0)
			require.NoError(t, err)
			defer selected.ReleaseFunc()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body))
			result, err := gateway.Forward(context.Background(), c, selected.Account, &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6-20260514", Stream: streaming})
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Contains(t, recorder.Body.String(), "tool_use")
			require.Contains(t, recorder.Body.String(), "next.txt")
			require.Equal(t, "claude-sonnet-4-6-20260514", result.Model)
			require.Equal(t, "claude-sonnet-4.6", result.UpstreamModel)
			require.Equal(t, 11, result.Usage.InputTokens)
			if streaming {
				require.Contains(t, recorder.Body.String(), "message_stop")
			}
			countAccount, err := gateway.SelectAccountForModel(context.Background(), &g.ID, "", "claude-sonnet-4-6-20260514")
			require.NoError(t, err)
			countRecorder := httptest.NewRecorder()
			countContext, _ := gin.CreateTestContext(countRecorder)
			countContext.Request = httptest.NewRequest("POST", "/v1/messages/count_tokens", bytes.NewReader(body))
			require.NoError(t, gateway.ForwardCountTokens(context.Background(), countContext, countAccount, &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6-20260514"}))
			require.Equal(t, 200, countRecorder.Code)
			require.Equal(t, "estimate", countRecorder.Header().Get("X-Sub2api-Usage-Source"))
			require.Contains(t, countRecorder.Body.String(), "input_tokens")
			require.Equal(t, 1, calls, "count_tokens must never send Kiro HTTP")
		})
	}
}

func TestKiroMixedSnapshotLoadsAndInvalidatesOldNewGroups(t *testing.T) {
	a, _ := kiroMixedFixture()
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "optout", true: "enabled"}[enabled], func(t *testing.T) {
			a.Extra["mixed_scheduling"] = enabled
			cache := newBulkEventSnapshotCache()
			repo := newBulkEventAccountRepo(a)
			s := newBulkEventTestService(cache, repo)
			require.NoError(t, s.handleBulkAccountEvent(context.Background(), bulkEventPayload([]int64{a.ID}, []int64{9}), make(map[batchSeenKey]struct{})))
			require.ElementsMatch(t, schedulerBucketsForTest([]int64{9, 10}, PlatformKiro, PlatformAnthropic), cache.capturedBuckets())
			single := newBulkEventSnapshotCache()
			s = newBulkEventTestService(single, repo)
			require.NoError(t, s.rebuildByAccount(context.Background(), a, []int64{9, 10}, "unit", nil))
			require.ElementsMatch(t, schedulerBucketsForTest([]int64{9, 10}, PlatformKiro, PlatformAnthropic), single.capturedBuckets())
		})
	}
	a.Extra["mixed_scheduling"] = true
	gateway := kiroMixedGateway(a, &Group{ID: 10}, false)
	snapshot := NewSchedulerSnapshotService(nil, nil, gateway.accountRepo, nil, gateway.cfg)
	accounts, err := snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 10, Platform: PlatformAnthropic, Mode: SchedulerModeMixed}, true)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	accounts, err = snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 99, Platform: PlatformAnthropic, Mode: SchedulerModeMixed}, true)
	require.NoError(t, err)
	require.Empty(t, accounts)
	accounts, err = snapshot.loadAccountsFromDB(context.Background(), SchedulerBucket{GroupID: 10, Platform: PlatformGemini, Mode: SchedulerModeMixed}, true)
	require.NoError(t, err)
	require.Empty(t, accounts)
}

type kiroMixedRiskRepo struct {
	*kiroMixedRepo
	writes int
}

func (r *kiroMixedRiskRepo) ListByGroup(context.Context, int64) ([]Account, error) {
	return []Account{{ID: 99, Platform: PlatformAnthropic}}, nil
}
func (r *kiroMixedRiskRepo) Update(context.Context, *Account) error { r.writes++; return nil }
func (r *kiroMixedRiskRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	r.writes++
	return 1, nil
}

func TestKiroMixedOnlyExtraUpdateAndBulkRiskBeforeWrites(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "update", true: "bulk"}[bulk], func(t *testing.T) {
			a, g := kiroMixedFixture()
			a.Extra = map[string]any{}
			gateway := kiroMixedGateway(a, g, false)
			repo := &kiroMixedRiskRepo{kiroMixedRepo: gateway.accountRepo.(*kiroMixedRepo)}
			admin := &adminServiceImpl{accountRepo: repo, groupRepo: gateway.groupRepo}
			var err error
			if bulk {
				_, err = admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{a.ID}, Extra: map[string]any{"mixed_scheduling": true}})
			} else {
				_, err = admin.UpdateAccount(context.Background(), a.ID, &UpdateAccountInput{Extra: map[string]any{"mixed_scheduling": true}})
			}
			var risk *MixedChannelError
			require.True(t, errors.As(err, &risk), "%v", err)
			require.Equal(t, g.ID, risk.GroupID, "use persisted membership even without input group_ids")
			require.Contains(t, risk.Error(), "not guaranteed")
			require.Zero(t, repo.writes)
		})
	}
}

func TestKiroMixedConfirmedUpdateAndBulkPreserveSettings(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		a, g := kiroMixedFixture()
		a.Extra = map[string]any{"retained_setting": "yes"}
		repo := &accountRepoStubForBulkUpdate{
			getByIDAccounts: map[int64]*Account{a.ID: a}, getByIDsAccounts: []*Account{a},
			listByGroupData: map[int64][]Account{g.ID: {{ID: 99, Platform: PlatformAnthropic}}},
		}
		admin := &adminServiceImpl{accountRepo: repo, groupRepo: &mockGroupRepoForGateway{groups: map[int64]*Group{g.ID: g}}}
		if bulk {
			_, err := admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
				AccountIDs: []int64{a.ID}, Extra: map[string]any{"mixed_scheduling": true}, SkipMixedChannelCheck: true,
			})
			require.NoError(t, err)
			require.Equal(t, map[string]any{"mixed_scheduling": true}, repo.lastBulkUpdate.Extra, "bulk remains a top-level delta")
			require.Nil(t, repo.lastBulkUpdate.Credentials)
			require.Empty(t, repo.bindGroupsCalls)
		} else {
			updated, err := admin.UpdateAccount(context.Background(), a.ID, &UpdateAccountInput{
				Extra: map[string]any{"mixed_scheduling": true, "retained_setting": "yes"}, SkipMixedChannelCheck: true,
			})
			require.NoError(t, err)
			require.Equal(t, []int64{g.ID}, updated.GroupIDs)
			require.Equal(t, "ksk_example", updated.GetCredential("api_key"))
			require.Equal(t, "yes", updated.Extra["retained_setting"])
			require.True(t, updated.IsKiroMixedSchedulingEnabled())
		}
	}
}

type kiroMixedEventRepo struct {
	*bulkEventAccountRepo
	account *Account
}

func (r *kiroMixedEventRepo) GetByID(context.Context, int64) (*Account, error) {
	if r.account == nil {
		return nil, ErrAccountNotFound
	}
	return r.account, nil
}

func TestKiroMixedSingleEventsInvalidateOldAndNewMembership(t *testing.T) {
	for _, state := range []string{"rebind", "unbind", "disabled", "deleted"} {
		t.Run(state, func(t *testing.T) {
			a, _ := kiroMixedFixture()
			switch state {
			case "unbind":
				a.GroupIDs, a.AccountGroups = nil, nil
			case "disabled":
				a.Status = "disabled"
			case "deleted":
				a = nil
			}
			cache := newBulkEventSnapshotCache()
			repo := &kiroMixedEventRepo{bulkEventAccountRepo: newBulkEventAccountRepo(), account: a}
			s := newBulkEventTestService(cache, repo)
			id := int64(42)
			require.NoError(t, s.handleAccountEvent(context.Background(), &id, map[string]any{"group_ids": []any{float64(9)}}, nil))
			set, deleted := cache.accountWrites()
			if a == nil {
				require.Equal(t, []int64{42}, deleted)
				require.Empty(t, set)
				require.ElementsMatch(t, schedulerBucketsForTest([]int64{9}, schedulerSnapshotPlatforms()...), cache.capturedBuckets())
			} else {
				groups := append([]int64{9}, a.GroupIDs...)
				require.ElementsMatch(t, schedulerBucketsForTest(groups, PlatformKiro, PlatformAnthropic), cache.capturedBuckets())
				require.Equal(t, []int64{42}, set)
				require.Empty(t, deleted)
			}
		})
	}
}
