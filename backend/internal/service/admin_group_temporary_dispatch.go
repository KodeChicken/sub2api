package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	MaxTemporaryDispatchGroups          = 100
	MaxTemporaryDispatchAccounts        = 20
	DefaultTemporaryDispatchDurationMin = 120
	MaxTemporaryDispatchDurationMin     = 24 * 60
	TemporaryDispatchModeTime           = "time"
	TemporaryDispatchModeUsage          = "usage"
	TemporaryDispatchModeHybrid         = "hybrid"
	TemporaryDispatchQuotaWindow5h      = "5h"
	TemporaryDispatchQuotaWindow7d      = "7d"
)

type StartTemporaryDispatchInput struct {
	GroupIDs []int64
	Accounts []TemporaryDispatchAccountInput
	// Legacy single-account fields remain accepted for API compatibility.
	AccountID          int64
	Mode               string
	DurationMinutes    int
	QuotaWindow        string
	TargetDeltaPercent float64
}

type TemporaryDispatchAccountInput struct {
	AccountID          int64   `json:"account_id"`
	DurationMinutes    int     `json:"duration_minutes,omitempty"`
	QuotaWindow        string  `json:"quota_window,omitempty"`
	TargetDeltaPercent float64 `json:"target_delta_percent,omitempty"`
}

type TemporaryDispatchAccountResult struct {
	AccountID       int64      `json:"account_id"`
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	QuotaWindow     string     `json:"quota_window,omitempty"`
	BaselinePercent *float64   `json:"baseline_percent,omitempty"`
	TargetPercent   *float64   `json:"target_percent,omitempty"`
	CurrentPercent  *float64   `json:"current_percent,omitempty"`
	QuotaResetAt    *time.Time `json:"quota_reset_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
}

type TemporaryDispatchResult struct {
	DispatchID      string                           `json:"dispatch_id"`
	GroupIDs        []int64                          `json:"group_ids"`
	AccountID       int64                            `json:"account_id"`
	Accounts        []TemporaryDispatchAccountResult `json:"accounts"`
	Mode            string                           `json:"mode"`
	QuotaWindow     string                           `json:"quota_window,omitempty"`
	BaselinePercent *float64                         `json:"baseline_percent,omitempty"`
	TargetPercent   *float64                         `json:"target_percent,omitempty"`
	CurrentPercent  *float64                         `json:"current_percent,omitempty"`
	QuotaResetAt    *time.Time                       `json:"quota_reset_at,omitempty"`
	StartedAt       time.Time                        `json:"started_at"`
	ExpiresAt       time.Time                        `json:"expires_at"`
}

type TemporaryDispatchAccountSpec struct {
	AccountID       int64      `json:"account_id"`
	Position        int        `json:"position"`
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	QuotaWindow     string     `json:"quota_window,omitempty"`
	BaselinePercent *float64   `json:"baseline_percent,omitempty"`
	TargetPercent   *float64   `json:"target_percent,omitempty"`
	CurrentPercent  *float64   `json:"current_percent,omitempty"`
	QuotaResetAt    *time.Time `json:"quota_reset_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
}

type TemporaryDispatchCreateSpec struct {
	DispatchID      string
	GroupIDs        []int64
	AccountID       int64
	Accounts        []TemporaryDispatchAccountSpec
	Mode            string
	QuotaWindow     string
	BaselinePercent *float64
	TargetPercent   *float64
	CurrentPercent  *float64
	QuotaResetAt    *time.Time
	StartedAt       time.Time
	ExpiresAt       time.Time
}

type temporaryDispatchRepository interface {
	SetTemporaryDispatch(ctx context.Context, groupIDs []int64, accountID int64, dispatchID string, startedAt, expiresAt time.Time) error
	ClearTemporaryDispatch(ctx context.Context, groupIDs []int64) error
}

func normalizeTemporaryDispatchGroupIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > MaxTemporaryDispatchGroups {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_GROUPS", fmt.Sprintf("group_ids must contain between 1 and %d items", MaxTemporaryDispatchGroups))
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_GROUPS", "group_ids must contain positive IDs")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func normalizeTemporaryDispatchAccounts(input StartTemporaryDispatchInput) ([]TemporaryDispatchAccountInput, error) {
	accounts := append([]TemporaryDispatchAccountInput(nil), input.Accounts...)
	if len(accounts) == 0 && input.AccountID > 0 {
		accounts = []TemporaryDispatchAccountInput{{
			AccountID:          input.AccountID,
			DurationMinutes:    input.DurationMinutes,
			QuotaWindow:        input.QuotaWindow,
			TargetDeltaPercent: input.TargetDeltaPercent,
		}}
	}
	if len(accounts) == 0 || len(accounts) > MaxTemporaryDispatchAccounts {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ACCOUNTS", fmt.Sprintf("accounts must contain between 1 and %d items", MaxTemporaryDispatchAccounts))
	}
	seen := make(map[int64]struct{}, len(accounts))
	out := make([]TemporaryDispatchAccountInput, 0, len(accounts))
	for _, account := range accounts {
		if account.AccountID <= 0 {
			return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ACCOUNT", "account_id must be positive")
		}
		if _, ok := seen[account.AccountID]; ok {
			return nil, infraerrors.BadRequest("DUPLICATE_TEMPORARY_DISPATCH_ACCOUNT", fmt.Sprintf("account %d is selected more than once", account.AccountID))
		}
		seen[account.AccountID] = struct{}{}
		out = append(out, account)
	}
	return out, nil
}

func newTemporaryDispatchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "td_" + hex.EncodeToString(raw[:]), nil
}

func (s *adminServiceImpl) StartTemporaryDispatch(ctx context.Context, input StartTemporaryDispatchInput) (*TemporaryDispatchResult, error) {
	if err := s.ValidateSimpleModeGroupOperation(AdminGroupOperationTemporaryDispatch); err != nil {
		return nil, err
	}
	groupIDs, err := normalizeTemporaryDispatchGroupIDs(input.GroupIDs)
	if err != nil {
		return nil, err
	}
	accountInputs, err := normalizeTemporaryDispatchAccounts(input)
	if err != nil {
		return nil, err
	}
	mode := input.Mode
	if mode == "" {
		mode = TemporaryDispatchModeTime
	}
	if mode != TemporaryDispatchModeTime && mode != TemporaryDispatchModeUsage && mode != TemporaryDispatchModeHybrid {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_MODE", "mode must be time, usage, or hybrid")
	}
	for i := range accountInputs {
		if mode == TemporaryDispatchModeTime || mode == TemporaryDispatchModeHybrid {
			if accountInputs[i].DurationMinutes == 0 {
				accountInputs[i].DurationMinutes = DefaultTemporaryDispatchDurationMin
			}
			if accountInputs[i].DurationMinutes < 1 || accountInputs[i].DurationMinutes > MaxTemporaryDispatchDurationMin {
				return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_DURATION", fmt.Sprintf("account %d duration_minutes must be between 1 and %d", accountInputs[i].AccountID, MaxTemporaryDispatchDurationMin))
			}
		}
		if mode == TemporaryDispatchModeUsage || mode == TemporaryDispatchModeHybrid {
			if accountInputs[i].QuotaWindow == "" {
				accountInputs[i].QuotaWindow = TemporaryDispatchQuotaWindow5h
			}
		}
	}

	accountByID := make(map[int64]*Account, len(accountInputs))
	if len(accountInputs) == 1 {
		account, getErr := s.accountRepo.GetByID(ctx, accountInputs[0].AccountID)
		if getErr != nil {
			return nil, getErr
		}
		accountByID[account.ID] = account
	} else {
		accountIDs := make([]int64, 0, len(accountInputs))
		for _, accountInput := range accountInputs {
			accountIDs = append(accountIDs, accountInput.AccountID)
		}
		accounts, getErr := s.accountRepo.GetByIDs(ctx, accountIDs)
		if getErr != nil {
			return nil, getErr
		}
		for _, account := range accounts {
			if account != nil {
				accountByID[account.ID] = account
			}
		}
		if len(accountByID) != len(accountInputs) {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_ACCOUNT_NOT_FOUND", "one or more target accounts no longer exist")
		}
	}

	groups := make([]*Group, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		group, getErr := s.groupRepo.GetByIDLite(ctx, groupID)
		if getErr != nil {
			return nil, getErr
		}
		if !group.IsActive() {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_GROUP_INACTIVE", fmt.Sprintf("group %d is inactive", groupID))
		}
		if group.Platform == PlatformComposite {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_COMPOSITE_UNSUPPORTED", fmt.Sprintf("group %d is composite and cannot be temporarily dispatched to one account", groupID))
		}
		if group.HasActiveTemporaryDispatch(time.Now().UTC()) {
			return nil, ErrTemporaryDispatchConflict
		}
		groups = append(groups, group)
	}

	quotaPlans := make(map[int64]*TemporaryDispatchQuotaPlan, len(accountInputs))
	for _, accountInput := range accountInputs {
		account := accountByID[accountInput.AccountID]
		if account == nil || !account.IsSchedulable() {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_ACCOUNT_UNAVAILABLE", fmt.Sprintf("account %d is not currently schedulable", accountInput.AccountID))
		}
		for _, group := range groups {
			if group.Platform != account.Platform {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_PLATFORM_MISMATCH", fmt.Sprintf("group %d platform %s does not match account %d platform %s", group.ID, group.Platform, account.ID, account.Platform))
			}
			if group.RequireOAuthOnly && account.Type == AccountTypeAPIKey {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_OAUTH_REQUIRED", fmt.Sprintf("group %d requires an OAuth account", group.ID))
			}
			if group.RequirePrivacySet && !account.IsPrivacySet() {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_PRIVACY_REQUIRED", fmt.Sprintf("group %d requires account %d to have privacy configured", group.ID, account.ID))
			}
		}
		if mode == TemporaryDispatchModeUsage || mode == TemporaryDispatchModeHybrid {
			if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNSUPPORTED", fmt.Sprintf("account %d must be a non-shadow OpenAI OAuth account for usage-based dispatch", account.ID))
			}
			if s.temporaryDispatchRuntime == nil {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNAVAILABLE", "temporary dispatch quota service is unavailable")
			}
			quotaPlan, prepareErr := s.temporaryDispatchRuntime.PrepareQuotaPlan(ctx, account.ID, accountInput.QuotaWindow, accountInput.TargetDeltaPercent)
			if prepareErr != nil {
				return nil, prepareErr
			}
			quotaPlans[account.ID] = quotaPlan
		}
	}

	repo, ok := s.groupRepo.(temporaryDispatchRepository)
	if !ok {
		return nil, fmt.Errorf("temporary dispatch repository is unavailable")
	}
	dispatchID, err := newTemporaryDispatchID()
	if err != nil {
		return nil, fmt.Errorf("generate temporary dispatch id: %w", err)
	}
	now := time.Now().UTC()
	members := make([]TemporaryDispatchAccountSpec, 0, len(accountInputs))
	var expiresAt time.Time
	for position, accountInput := range accountInputs {
		member := TemporaryDispatchAccountSpec{
			AccountID: accountInput.AccountID, Position: position,
			DurationMinutes: accountInput.DurationMinutes,
		}
		if mode == TemporaryDispatchModeTime || mode == TemporaryDispatchModeHybrid {
			member.ExpiresAt = now.Add(time.Duration(accountInput.DurationMinutes) * time.Minute)
		}
		if quotaPlan := quotaPlans[accountInput.AccountID]; quotaPlan != nil {
			member.QuotaWindow = quotaPlan.Window
			member.BaselinePercent = &quotaPlan.BaselinePercent
			member.TargetPercent = &quotaPlan.TargetPercent
			member.CurrentPercent = &quotaPlan.BaselinePercent
			member.QuotaResetAt = &quotaPlan.ResetAt
			if mode == TemporaryDispatchModeUsage || quotaPlan.ResetAt.Before(member.ExpiresAt) {
				member.ExpiresAt = quotaPlan.ResetAt
			}
		}
		if !member.ExpiresAt.After(now) {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_ACCOUNT_DEADLINE_ELAPSED", fmt.Sprintf("account %d temporary dispatch deadline has already elapsed", accountInput.AccountID))
		}
		if member.ExpiresAt.After(expiresAt) {
			expiresAt = member.ExpiresAt
		}
		members = append(members, member)
	}
	primary := members[0]
	spec := TemporaryDispatchCreateSpec{
		DispatchID: dispatchID, GroupIDs: groupIDs, AccountID: primary.AccountID, Accounts: members, Mode: mode,
		StartedAt: now, ExpiresAt: expiresAt,
	}
	spec.QuotaWindow = primary.QuotaWindow
	spec.BaselinePercent = primary.BaselinePercent
	spec.TargetPercent = primary.TargetPercent
	spec.CurrentPercent = primary.CurrentPercent
	spec.QuotaResetAt = primary.QuotaResetAt
	if s.temporaryDispatchRuntime != nil {
		if err := s.temporaryDispatchRuntime.Create(ctx, spec); err != nil {
			return nil, err
		}
	} else {
		if len(members) != 1 {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_POOL_UNAVAILABLE", "multi-account temporary dispatch runtime is unavailable")
		}
		if err := repo.SetTemporaryDispatch(ctx, groupIDs, primary.AccountID, dispatchID, now, expiresAt); err != nil {
			return nil, err
		}
	}
	for _, groupID := range groupIDs {
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		}
	}
	result := &TemporaryDispatchResult{
		DispatchID: dispatchID, GroupIDs: groupIDs, AccountID: primary.AccountID, Mode: mode,
		StartedAt: now, ExpiresAt: expiresAt,
	}
	result.QuotaWindow = primary.QuotaWindow
	result.BaselinePercent = primary.BaselinePercent
	result.TargetPercent = primary.TargetPercent
	result.CurrentPercent = primary.CurrentPercent
	result.QuotaResetAt = primary.QuotaResetAt
	for _, member := range members {
		result.Accounts = append(result.Accounts, TemporaryDispatchAccountResult{
			AccountID: member.AccountID, DurationMinutes: member.DurationMinutes,
			QuotaWindow: member.QuotaWindow, BaselinePercent: member.BaselinePercent,
			TargetPercent: member.TargetPercent, CurrentPercent: member.CurrentPercent,
			QuotaResetAt: member.QuotaResetAt, ExpiresAt: member.ExpiresAt,
		})
	}
	return result, nil
}

func (s *adminServiceImpl) GetTemporaryDispatchQuotaPreview(ctx context.Context, accountID int64, window string) (*TemporaryDispatchQuotaPreview, error) {
	if accountID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ACCOUNT", "account_id must be positive")
	}
	if s.temporaryDispatchRuntime == nil {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNAVAILABLE", "temporary dispatch quota service is unavailable")
	}
	return s.temporaryDispatchRuntime.GetQuotaPreview(ctx, accountID, window)
}

func (s *adminServiceImpl) StopTemporaryDispatch(ctx context.Context, ids []int64) error {
	if err := s.ValidateSimpleModeGroupOperation(AdminGroupOperationTemporaryDispatch); err != nil {
		return err
	}
	groupIDs, err := normalizeTemporaryDispatchGroupIDs(ids)
	if err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
			return err
		}
	}
	repo, ok := s.groupRepo.(temporaryDispatchRepository)
	if !ok {
		return fmt.Errorf("temporary dispatch repository is unavailable")
	}
	if s.temporaryDispatchRuntime != nil {
		if err := s.temporaryDispatchRuntime.StopGroups(ctx, groupIDs); err != nil {
			return err
		}
	} else if err := repo.ClearTemporaryDispatch(ctx, groupIDs); err != nil {
		return err
	}
	for _, groupID := range groupIDs {
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		}
	}
	return nil
}
