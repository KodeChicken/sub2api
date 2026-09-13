package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func activeTemporaryDispatchGroup(ctx context.Context, groupID *int64) (*Group, bool) {
	if ctx == nil || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || !IsGroupContextValid(group) || group.ID != *groupID || !group.HasActiveTemporaryDispatch(time.Now()) {
		return nil, false
	}
	return group, true
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
	accountID := *group.TemporaryDispatchAccountID
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
