package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
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
	TemporaryDispatchUsageQuotaPercent  = "quota_percent"
	TemporaryDispatchUsageAccountCost   = "account_cost"
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
	TargetCost         float64
}

type TemporaryDispatchAccountInput struct {
	AccountID          int64   `json:"account_id"`
	DurationMinutes    int     `json:"duration_minutes,omitempty"`
	QuotaWindow        string  `json:"quota_window,omitempty"`
	TargetDeltaPercent float64 `json:"target_delta_percent,omitempty"`
	TargetCost         float64 `json:"target_cost,omitempty"`
}

type TemporaryDispatchAccountResult struct {
	AccountID       int64      `json:"account_id"`
	AccountName     string     `json:"account_name,omitempty"`
	AccountType     string     `json:"account_type,omitempty"`
	DurationMinutes int        `json:"duration_minutes,omitempty"`
	UsageMetric     string     `json:"usage_metric,omitempty"`
	QuotaWindow     string     `json:"quota_window,omitempty"`
	BaselinePercent *float64   `json:"baseline_percent,omitempty"`
	TargetPercent   *float64   `json:"target_percent,omitempty"`
	CurrentPercent  *float64   `json:"current_percent,omitempty"`
	QuotaResetAt    *time.Time `json:"quota_reset_at,omitempty"`
	ExpiresAt       time.Time  `json:"expires_at"`
}

type AdjustTemporaryDispatchInput struct {
	GroupID  int64
	Accounts []TemporaryDispatchAdjustment
}

type TemporaryDispatchAdjustment struct {
	AccountID          int64   `json:"account_id"`
	AdditionalUsage    float64 `json:"additional_usage,omitempty"`
	ExtendDurationMins int     `json:"extend_duration_minutes,omitempty"`
}

type TemporaryDispatchResult struct {
	DispatchID      string                           `json:"dispatch_id"`
	GroupIDs        []int64                          `json:"group_ids"`
	AccountID       int64                            `json:"account_id"`
	Accounts        []TemporaryDispatchAccountResult `json:"accounts"`
	Mode            string                           `json:"mode"`
	UsageMetric     string                           `json:"usage_metric,omitempty"`
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
	UsageMetric     string     `json:"usage_metric,omitempty"`
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
	UsageMetric     string
	QuotaWindow     string
	BaselinePercent *float64
	TargetPercent   *float64
	CurrentPercent  *float64
	QuotaResetAt    *time.Time
	StartedAt       time.Time
	ExpiresAt       time.Time
}

func temporaryDispatchWindowDuration(window string) time.Duration {
	if window == TemporaryDispatchQuotaWindow7d {
		return 7 * 24 * time.Hour
	}
	return 5 * time.Hour
}

type temporaryDispatchRepository interface {
	SetTemporaryDispatch(ctx context.Context, groupIDs []int64, accountID int64, dispatchID string, startedAt, expiresAt time.Time) error
	ClearTemporaryDispatch(ctx context.Context, groupIDs []int64) error
}

type temporaryDispatchEditorRepository interface {
	GetTemporaryDispatchByGroupID(ctx context.Context, groupID int64) (*TemporaryDispatchResult, error)
	AdjustTemporaryDispatch(ctx context.Context, input AdjustTemporaryDispatchInput) ([]int64, error)
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
			TargetCost:         input.TargetCost,
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
	if (mode == TemporaryDispatchModeUsage || mode == TemporaryDispatchModeHybrid) && s.temporaryDispatchRuntime == nil {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_USAGE_UNAVAILABLE", "temporary dispatch usage tracking service is unavailable")
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
	usageMetrics := make(map[int64]string, len(accountInputs))
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
			if account.Platform != PlatformOpenAI {
				return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_USAGE_UNSUPPORTED", fmt.Sprintf("account %d must be an OpenAI account for usage-based dispatch", account.ID))
			}
			if account.Type == AccountTypeOAuth && !account.IsShadow() {
				quotaPlan, prepareErr := s.temporaryDispatchRuntime.PrepareQuotaPlan(ctx, account.ID, accountInput.QuotaWindow, accountInput.TargetDeltaPercent)
				if prepareErr != nil {
					return nil, prepareErr
				}
				quotaPlans[account.ID] = quotaPlan
				usageMetrics[account.ID] = TemporaryDispatchUsageQuotaPercent
			} else {
				if accountInput.QuotaWindow != TemporaryDispatchQuotaWindow5h && accountInput.QuotaWindow != TemporaryDispatchQuotaWindow7d {
					return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_QUOTA_WINDOW", "quota_window must be 5h or 7d")
				}
				if math.IsNaN(accountInput.TargetCost) || math.IsInf(accountInput.TargetCost, 0) || accountInput.TargetCost <= 0 {
					return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_TARGET_COST", fmt.Sprintf("account %d target_cost must be greater than 0", account.ID))
				}
				usageMetrics[account.ID] = TemporaryDispatchUsageAccountCost
			}
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
			DurationMinutes: accountInput.DurationMinutes, UsageMetric: usageMetrics[accountInput.AccountID],
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
		} else if member.UsageMetric == TemporaryDispatchUsageAccountCost {
			baseline, target, current := 0.0, accountInput.TargetCost, 0.0
			resetAt := now.Add(temporaryDispatchWindowDuration(accountInput.QuotaWindow))
			member.QuotaWindow = accountInput.QuotaWindow
			member.BaselinePercent = &baseline
			member.TargetPercent = &target
			member.CurrentPercent = &current
			member.QuotaResetAt = &resetAt
			if mode == TemporaryDispatchModeUsage || resetAt.Before(member.ExpiresAt) {
				member.ExpiresAt = resetAt
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
		UsageMetric: primary.UsageMetric, StartedAt: now, ExpiresAt: expiresAt,
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
		UsageMetric: primary.UsageMetric, StartedAt: now, ExpiresAt: expiresAt,
	}
	result.QuotaWindow = primary.QuotaWindow
	result.BaselinePercent = primary.BaselinePercent
	result.TargetPercent = primary.TargetPercent
	result.CurrentPercent = primary.CurrentPercent
	result.QuotaResetAt = primary.QuotaResetAt
	for _, member := range members {
		account := accountByID[member.AccountID]
		result.Accounts = append(result.Accounts, TemporaryDispatchAccountResult{
			AccountID: member.AccountID, AccountName: account.Name, AccountType: account.Type,
			DurationMinutes: member.DurationMinutes,
			UsageMetric:     member.UsageMetric, QuotaWindow: member.QuotaWindow, BaselinePercent: member.BaselinePercent,
			TargetPercent: member.TargetPercent, CurrentPercent: member.CurrentPercent,
			QuotaResetAt: member.QuotaResetAt, ExpiresAt: member.ExpiresAt,
		})
	}
	return result, nil
}

func (s *adminServiceImpl) GetTemporaryDispatch(ctx context.Context, groupID int64) (*TemporaryDispatchResult, error) {
	if groupID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_GROUP", "group_id must be positive")
	}
	repo, ok := s.groupRepo.(temporaryDispatchEditorRepository)
	if !ok {
		return nil, fmt.Errorf("temporary dispatch editor is unavailable")
	}
	result, err := repo.GetTemporaryDispatchByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	accountIDs := make([]int64, 0, len(result.Accounts))
	for _, account := range result.Accounts {
		accountIDs = append(accountIDs, account.AccountID)
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, accountIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	for i := range result.Accounts {
		if account := byID[result.Accounts[i].AccountID]; account != nil {
			result.Accounts[i].AccountName = account.Name
			result.Accounts[i].AccountType = account.Type
		}
	}
	return result, nil
}

func (s *adminServiceImpl) AdjustTemporaryDispatch(ctx context.Context, input AdjustTemporaryDispatchInput) (*TemporaryDispatchResult, error) {
	if err := s.ValidateSimpleModeGroupOperation(AdminGroupOperationTemporaryDispatch); err != nil {
		return nil, err
	}
	if input.GroupID <= 0 || len(input.Accounts) == 0 || len(input.Accounts) > MaxTemporaryDispatchAccounts {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ADJUSTMENT", "group_id and at least one account adjustment are required")
	}
	seen := make(map[int64]struct{}, len(input.Accounts))
	for _, adjustment := range input.Accounts {
		if adjustment.AccountID <= 0 || math.IsNaN(adjustment.AdditionalUsage) || math.IsInf(adjustment.AdditionalUsage, 0) || adjustment.AdditionalUsage < 0 || adjustment.ExtendDurationMins < 0 {
			return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ADJUSTMENT", "adjustments must contain valid non-negative values")
		}
		if adjustment.AdditionalUsage == 0 && adjustment.ExtendDurationMins == 0 {
			return nil, infraerrors.BadRequest("EMPTY_TEMPORARY_DISPATCH_ADJUSTMENT", fmt.Sprintf("account %d has no adjustment", adjustment.AccountID))
		}
		if adjustment.ExtendDurationMins > MaxTemporaryDispatchDurationMin {
			return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_DURATION", fmt.Sprintf("account %d extension cannot exceed %d minutes", adjustment.AccountID, MaxTemporaryDispatchDurationMin))
		}
		if _, exists := seen[adjustment.AccountID]; exists {
			return nil, infraerrors.BadRequest("DUPLICATE_TEMPORARY_DISPATCH_ACCOUNT", fmt.Sprintf("account %d is adjusted more than once", adjustment.AccountID))
		}
		seen[adjustment.AccountID] = struct{}{}
	}
	repo, ok := s.groupRepo.(temporaryDispatchEditorRepository)
	if !ok {
		return nil, fmt.Errorf("temporary dispatch editor is unavailable")
	}
	groupIDs, err := repo.AdjustTemporaryDispatch(ctx, input)
	if err != nil {
		return nil, err
	}
	for _, groupID := range groupIDs {
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		}
	}
	return s.GetTemporaryDispatch(ctx, input.GroupID)
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
