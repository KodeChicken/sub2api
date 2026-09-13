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
	DefaultTemporaryDispatchDurationMin = 120
	MaxTemporaryDispatchDurationMin     = 24 * 60
	TemporaryDispatchModeTime           = "time"
	TemporaryDispatchModeUsage          = "usage"
	TemporaryDispatchModeHybrid         = "hybrid"
	TemporaryDispatchQuotaWindow5h      = "5h"
	TemporaryDispatchQuotaWindow7d      = "7d"
)

type StartTemporaryDispatchInput struct {
	GroupIDs           []int64
	AccountID          int64
	Mode               string
	DurationMinutes    int
	QuotaWindow        string
	TargetDeltaPercent float64
}

type TemporaryDispatchResult struct {
	DispatchID      string     `json:"dispatch_id"`
	GroupIDs        []int64    `json:"group_ids"`
	AccountID       int64      `json:"account_id"`
	Mode            string     `json:"mode"`
	QuotaWindow     string     `json:"quota_window,omitempty"`
	BaselinePercent *float64   `json:"baseline_percent,omitempty"`
	TargetPercent   *float64   `json:"target_percent,omitempty"`
	CurrentPercent  *float64   `json:"current_percent,omitempty"`
	QuotaResetAt    *time.Time `json:"quota_reset_at,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	ExpiresAt       time.Time  `json:"expires_at"`
}

type TemporaryDispatchCreateSpec struct {
	DispatchID      string
	GroupIDs        []int64
	AccountID       int64
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
	if input.AccountID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_ACCOUNT", "account_id must be positive")
	}
	mode := input.Mode
	if mode == "" {
		mode = TemporaryDispatchModeTime
	}
	if mode != TemporaryDispatchModeTime && mode != TemporaryDispatchModeUsage && mode != TemporaryDispatchModeHybrid {
		return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_MODE", "mode must be time, usage, or hybrid")
	}
	duration := input.DurationMinutes
	if mode == TemporaryDispatchModeTime || mode == TemporaryDispatchModeHybrid {
		if duration == 0 {
			duration = DefaultTemporaryDispatchDurationMin
		}
		if duration < 1 || duration > MaxTemporaryDispatchDurationMin {
			return nil, infraerrors.BadRequest("INVALID_TEMPORARY_DISPATCH_DURATION", fmt.Sprintf("duration_minutes must be between 1 and %d", MaxTemporaryDispatchDurationMin))
		}
	}

	account, err := s.accountRepo.GetByID(ctx, input.AccountID)
	if err != nil {
		return nil, err
	}
	if !account.IsSchedulable() {
		return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_ACCOUNT_UNAVAILABLE", "target account is not currently schedulable")
	}

	now := time.Now().UTC()
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
		if group.Platform != account.Platform {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_PLATFORM_MISMATCH", fmt.Sprintf("group %d platform %s does not match account platform %s", groupID, group.Platform, account.Platform))
		}
		if group.RequireOAuthOnly && account.Type == AccountTypeAPIKey {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_OAUTH_REQUIRED", fmt.Sprintf("group %d requires an OAuth account", groupID))
		}
		if group.RequirePrivacySet && !account.IsPrivacySet() {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_PRIVACY_REQUIRED", fmt.Sprintf("group %d requires an account with privacy configured", groupID))
		}
		if group.HasActiveTemporaryDispatch(now) {
			return nil, ErrTemporaryDispatchConflict
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
	var quotaPlan *TemporaryDispatchQuotaPlan
	if mode == TemporaryDispatchModeUsage || mode == TemporaryDispatchModeHybrid {
		if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNSUPPORTED", "usage-based temporary dispatch requires a non-shadow OpenAI OAuth account")
		}
		if s.temporaryDispatchRuntime == nil {
			return nil, infraerrors.BadRequest("TEMPORARY_DISPATCH_QUOTA_UNAVAILABLE", "temporary dispatch quota service is unavailable")
		}
		quotaPlan, err = s.temporaryDispatchRuntime.PrepareQuotaPlan(ctx, account.ID, input.QuotaWindow, input.TargetDeltaPercent)
		if err != nil {
			return nil, err
		}
	}

	expiresAt := now.Add(time.Duration(duration) * time.Minute)
	if mode == TemporaryDispatchModeUsage {
		expiresAt = quotaPlan.ResetAt
	} else if mode == TemporaryDispatchModeHybrid && quotaPlan.ResetAt.Before(expiresAt) {
		expiresAt = quotaPlan.ResetAt
	}
	spec := TemporaryDispatchCreateSpec{
		DispatchID: dispatchID, GroupIDs: groupIDs, AccountID: account.ID, Mode: mode,
		StartedAt: now, ExpiresAt: expiresAt,
	}
	if quotaPlan != nil {
		spec.QuotaWindow = quotaPlan.Window
		spec.BaselinePercent = &quotaPlan.BaselinePercent
		spec.TargetPercent = &quotaPlan.TargetPercent
		spec.CurrentPercent = &quotaPlan.BaselinePercent
		spec.QuotaResetAt = &quotaPlan.ResetAt
	}
	if s.temporaryDispatchRuntime != nil {
		if err := s.temporaryDispatchRuntime.Create(ctx, spec); err != nil {
			return nil, err
		}
	} else if err := repo.SetTemporaryDispatch(ctx, groupIDs, account.ID, dispatchID, now, expiresAt); err != nil {
		return nil, err
	}
	for _, groupID := range groupIDs {
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, groupID)
		}
	}
	result := &TemporaryDispatchResult{
		DispatchID: dispatchID, GroupIDs: groupIDs, AccountID: account.ID, Mode: mode,
		StartedAt: now, ExpiresAt: expiresAt,
	}
	if quotaPlan != nil {
		result.QuotaWindow = quotaPlan.Window
		result.BaselinePercent = &quotaPlan.BaselinePercent
		result.TargetPercent = &quotaPlan.TargetPercent
		result.CurrentPercent = &quotaPlan.BaselinePercent
		result.QuotaResetAt = &quotaPlan.ResetAt
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
