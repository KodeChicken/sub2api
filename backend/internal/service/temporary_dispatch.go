package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type temporaryDispatchBypassContextKey struct{}

// withTemporaryDispatchBypass disables the temporary overlay for one
// scheduling pass. It is used only after every temporary target has failed to
// acquire a concurrency slot, allowing the request to spill back to the
// group's permanent pool without ending the dispatch.
func withTemporaryDispatchBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, temporaryDispatchBypassContextKey{}, true)
}

func temporaryDispatchBypassed(ctx context.Context) bool {
	bypassed, _ := ctx.Value(temporaryDispatchBypassContextKey{}).(bool)
	return bypassed
}

func activeTemporaryDispatchGroup(ctx context.Context, groupID *int64) (*Group, bool) {
	if ctx == nil || temporaryDispatchBypassed(ctx) || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || !IsGroupContextValid(group) || group.ID != *groupID || !group.HasActiveTemporaryDispatch(time.Now()) {
		return nil, false
	}
	return group, true
}

func markTemporaryDispatchSelection(ctx context.Context, result *AccountSelectionResult) *AccountSelectionResult {
	if result == nil || result.Account == nil || temporaryDispatchBypassed(ctx) {
		return result
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || !IsGroupContextValid(group) || !group.HasActiveTemporaryDispatch(time.Now()) ||
		!group.IsTemporaryDispatchAccount(result.Account.ID) {
		return result
	}
	result.temporaryDispatch = true
	result.temporaryDispatchAccountIDs = group.TemporaryDispatchAccountPool()
	return result
}

func temporaryDispatchOverflow(selection *AccountSelectionResult) ([]int64, bool) {
	if selection == nil || !selection.temporaryDispatch || selection.WaitPlan == nil ||
		selection.WaitPlan.AccountID <= 0 || len(selection.temporaryDispatchAccountIDs) == 0 {
		return nil, false
	}
	return selection.temporaryDispatchAccountIDs, true
}

func mergeTemporaryDispatchExclusions(excludedIDs map[int64]struct{}, accountIDs []int64) map[int64]struct{} {
	merged := make(map[int64]struct{}, len(excludedIDs)+len(accountIDs))
	for accountID := range excludedIDs {
		merged[accountID] = struct{}{}
	}
	for _, accountID := range accountIDs {
		if accountID > 0 {
			merged[accountID] = struct{}{}
		}
	}
	return merged
}

func allTemporaryDispatchTargetsExcluded(accountIDs []int64, excludedIDs map[int64]struct{}) bool {
	for _, accountID := range accountIDs {
		if _, excluded := excludedIDs[accountID]; !excluded {
			return false
		}
	}
	return true
}

func temporaryDispatchSelectionError(model, reason string) error {
	if model == "" {
		return fmt.Errorf("%w (temporary dispatch: %s)", ErrNoAvailableAccounts, reason)
	}
	return fmt.Errorf("%w supporting model: %s (temporary dispatch: %s)", ErrNoAvailableAccounts, model, reason)
}

// temporaryDispatchAccount performs the common immutable checks. handled=false
// means the rule has ended operationally (expired, deleted/disabled target or
// platform changed), so normal group routing resumes without rewriting binds.
func temporaryDispatchAccount(ctx context.Context, repo AccountRepository, groupID *int64, platform, requestedModel string, excludedIDs map[int64]struct{}) (*Account, bool, error) {
	group, active := activeTemporaryDispatchGroup(ctx, groupID)
	if !active || repo == nil {
		return nil, false, nil
	}
	pool := group.TemporaryDispatchAccountPool()
	if len(pool) != 1 {
		// Multi-account overlays continue through the normal scheduler with a
		// candidate-pool override supplied by temporaryDispatchPoolAccounts.
		return nil, false, nil
	}
	accountID := pool[0]
	account, err := repo.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			notifyTemporaryDispatchAccountUnavailable(accountID, "account_deleted")
			return nil, false, nil
		}
		return nil, true, err
	}
	if !account.IsActive() || !account.Schedulable || account.Platform != platform ||
		(account.AutoPauseOnExpired && account.ExpiresAt != nil && !account.ExpiresAt.After(time.Now())) {
		notifyTemporaryDispatchAccountUnavailable(accountID, "account_unavailable")
		return nil, false, nil
	}
	if _, excluded := excludedIDs[account.ID]; excluded {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account already failed this request")
	}
	return account, true, nil
}

// temporaryDispatchPoolAccounts loads an unbound multi-account overlay for the
// normal scheduler. handled=false with an active but empty pool deliberately
// lets the current request resume the original group while async cleanup
// removes the final unavailable members.
func temporaryDispatchPoolAccounts(ctx context.Context, repo AccountRepository, groupID *int64, platform string) ([]Account, bool, error) {
	group, active := activeTemporaryDispatchGroup(ctx, groupID)
	if !active || repo == nil {
		return nil, false, nil
	}
	pool := group.TemporaryDispatchAccountPool()
	if len(pool) <= 1 {
		return nil, false, nil
	}
	loaded, err := repo.GetByIDs(ctx, pool)
	if err != nil {
		return nil, true, err
	}
	byID := make(map[int64]*Account, len(loaded))
	for _, account := range loaded {
		if account != nil {
			byID[account.ID] = account
		}
	}
	accounts := make([]Account, 0, len(pool))
	for _, accountID := range pool {
		account := byID[accountID]
		if account == nil {
			notifyTemporaryDispatchAccountUnavailable(accountID, "account_deleted")
			continue
		}
		if account.Platform != platform || !account.IsSchedulable() {
			notifyTemporaryDispatchAccountUnavailable(accountID, "account_unavailable")
			continue
		}
		candidate := *account
		if groupID != nil {
			candidate.GroupIDs = append([]int64(nil), account.GroupIDs...)
			candidate.GroupIDs = append(candidate.GroupIDs, *groupID)
			candidate.AccountGroups = append([]AccountGroup(nil), account.AccountGroups...)
			candidate.AccountGroups = append(candidate.AccountGroups, AccountGroup{
				AccountID: candidate.ID, GroupID: *groupID, Priority: candidate.Priority,
			})
		}
		accounts = append(accounts, candidate)
	}
	if len(accounts) == 0 {
		return nil, false, nil
	}
	return accounts, true, nil
}

func temporaryDispatchAllowsAccount(ctx context.Context, groupID *int64, accountID int64) (bool, bool) {
	group, active := activeTemporaryDispatchGroup(ctx, groupID)
	if !active {
		return false, false
	}
	return group.IsTemporaryDispatchAccount(accountID), true
}

func (s *GatewayService) selectTemporaryDispatchAccount(ctx context.Context, groupID *int64, group *Group, platform, requestedModel string, excludedIDs map[int64]struct{}) (*Account, bool, error) {
	account, handled, err := temporaryDispatchAccount(ctx, s.accountRepo, groupID, platform, requestedModel, excludedIDs)
	if err != nil || !handled {
		return account, handled, err
	}
	if !s.isAccountSchedulableForSelection(account) ||
		!s.isAccountSchedulableForModelSelection(ctx, account, requestedModel) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is temporarily unavailable")
	}
	if !s.isAccountSchedulableForQuota(account) {
		// Quota exhaustion is terminal for temporary dispatch. Routing resumes
		// immediately and persisted metadata is removed asynchronously.
		notifyTemporaryDispatchAccountUnavailable(account.ID, "quota_paused")
		return nil, false, nil
	}
	if group != nil && group.RequireOAuthOnly && account.Type == AccountTypeAPIKey {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account violates the group's OAuth-only policy")
	}
	if group != nil && group.RequirePrivacySet && !account.IsPrivacySet() {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account does not satisfy the group's privacy policy")
	}
	if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, account, requestedModel) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account does not support the model")
	}
	if !s.isGatewayAccountProfitEligible(ctx, account) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is blocked by profit control")
	}
	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is blocked by channel restrictions")
	}
	return account, true, nil
}

func (s *OpenAIGatewayService) selectTemporaryDispatchAccount(ctx context.Context, groupID *int64, platform, requestedModel string, excludedIDs map[int64]struct{}, requiredTransport OpenAIUpstreamTransport, requiredCapability OpenAIEndpointCapability, requiredImageCapability OpenAIImagesCapability, requireCompact bool) (*Account, bool, error) {
	platform = NormalizeOpenAICompatiblePlatform(platform)
	account, handled, err := temporaryDispatchAccount(ctx, s.accountRepo, groupID, platform, requestedModel, excludedIDs)
	if err != nil || !handled {
		return account, handled, err
	}
	if account.IsOpenAI() {
		if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
			notifyTemporaryDispatchAccountUnavailable(account.ID, "quota_paused")
			return nil, false, nil
		}
	}
	if account.IsGrok() {
		if paused, _ := shouldAutoPauseGrokAccountByQuota(account); paused {
			notifyTemporaryDispatchAccountUnavailable(account.ID, "quota_paused")
			return nil, false, nil
		}
	}
	group, _ := activeTemporaryDispatchGroup(ctx, groupID)
	if group != nil && group.RequireOAuthOnly && account.Type == AccountTypeAPIKey {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account violates the group's OAuth-only policy")
	}
	if group != nil && group.RequirePrivacySet && !account.IsPrivacySet() {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account does not satisfy the group's privacy policy")
	}
	if !isOpenAICompatibleAccountEligibleForRequest(ctx, account, platform, requestedModel, requireCompact, requiredCapability) ||
		!accountSupportsOpenAICapabilities(account, requiredCapability, requiredImageCapability) ||
		!s.isOpenAIAccountTransportCompatible(account, requiredTransport) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is incompatible or temporarily unavailable")
	}
	if !parentHealthyForShadow(account, s.parentAccountLookup(ctx)) {
		return nil, false, nil
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(account, requestedModel) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is temporarily blocked")
	}
	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel, requireCompact) {
		return nil, true, temporaryDispatchSelectionError(requestedModel, "target account is blocked by channel restrictions")
	}
	return account, true, nil
}
