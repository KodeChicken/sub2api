package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

const carpoolQuotaResetStateExtraKey = "carpool_quota_reset_state"

// Only quota observations advance this baseline; display snapshots never do.
type carpoolQuotaResetState struct {
	Baseline  map[string]any `json:"baseline"`
	CheckedAt string         `json:"checked_at"`
}

func carpoolQuotaResetStateFromExtra(extra map[string]any) *carpoolQuotaResetState {
	raw, ok := extra[carpoolQuotaResetStateExtraKey]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var state carpoolQuotaResetState
	if err := json.Unmarshal(encoded, &state); err != nil || state.Baseline == nil {
		return nil
	}
	return &state
}

func carpoolQuotaBaseline(updates map[string]any) map[string]any {
	baseline := make(map[string]any, 3)
	for _, key := range []string{"codex_7d_used_percent", "codex_7d_reset_at", "codex_usage_updated_at"} {
		if value, ok := updates[key]; ok {
			baseline[key] = value
		}
	}
	return baseline
}

func nextCarpoolQuotaResetState(previous map[string]any, updates map[string]any, decision carpool7DResetDecision, observedAt time.Time) *carpoolQuotaResetState {
	baseline := carpoolQuotaBaseline(updates)
	if decision.Pending != nil {
		baseline = carpoolQuotaBaseline(previous)
		baseline[carpoolForcedResetCandidateExtraKey] = decision.Pending
	}
	return &carpoolQuotaResetState{Baseline: baseline, CheckedAt: observedAt.UTC().Format(time.RFC3339Nano)}
}

// ObserveCarpoolQuotaSnapshot serializes detection across instances and commits
// the baseline together with the quota reset, retaining it on any write failure.
func (s *SubscriptionService) ObserveCarpoolQuotaSnapshot(ctx context.Context, accountID int64, updates map[string]any, now time.Time) (*CarpoolQuotaResetResult, error) {
	if s == nil || s.entClient == nil || accountID <= 0 {
		return nil, ErrInvalidInput
	}
	if _, ok := updates["codex_7d_used_percent"]; !ok {
		return nil, nil
	}
	observedAt, ok := parseCarpoolResetTime(updates["codex_usage_updated_at"])
	if !ok {
		return nil, errors.New("carpool quota observation has no valid timestamp")
	}
	var result *CarpoolQuotaResetResult
	var users map[int64]int64
	var decision carpool7DResetDecision
	var previousUsed float64
	var skippedManualReset bool
	err := s.withCarpoolQuotaTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		var rawExtra []byte
		err := scanCarpoolQueryRow(txCtx, client, `
SELECT COALESCE(extra, '{}'::jsonb) FROM accounts
WHERE id=$1 AND deleted_at IS NULL AND platform='openai' AND type='oauth'
  AND status='active' AND parent_account_id IS NULL
FOR UPDATE`, []any{&rawExtra}, accountID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var extra map[string]any
		if err := json.Unmarshal(rawExtra, &extra); err != nil {
			return fmt.Errorf("decode carpool account extra: %w", err)
		}
		state := carpoolQuotaResetStateFromExtra(extra)
		previous := carpoolQuotaBaseline(extra)
		if state != nil {
			if checkedAt, ok := parseCarpoolResetTime(state.CheckedAt); ok && !observedAt.After(checkedAt) {
				return nil
			}
			previous = state.Baseline
		} else if candidate, ok := extra[carpoolForcedResetCandidateExtraKey]; ok {
			previous[carpoolForcedResetCandidateExtraKey] = candidate
		}
		var count int
		if err := scanCarpoolQueryRow(txCtx, client, carpoolSubscriptionCountSQL, []any{&count}, accountID, now); err != nil || count == 0 {
			return err
		}
		since, ok := parseCarpoolResetTime(previous["codex_usage_updated_at"])
		if !ok {
			since = observedAt.Add(-carpoolQuotaSnapshotInterval)
		}
		var resetAt sql.NullTime
		if err := scanCarpoolQueryRow(txCtx, client, `
SELECT MAX(created_at) FROM carpool_subscription_quota_reset_events
WHERE account_id=$1 AND source IN ($2, $3, $4) AND status='applied'
  AND created_at > $5`, []any{&resetAt}, accountID,
			CarpoolResetSourceManualCard, CarpoolResetSourceAutoCard, CarpoolResetSourceFallback, since); err != nil {
			return err
		}
		if resetAt.Valid && !observedAt.After(resetAt.Time) {
			return nil
		}
		skippedManualReset = resetAt.Valid
		previousUsed = readOpenAIQuotaUsedPercent(previous, "7d")
		if !skippedManualReset {
			decision = assessCarpool7DWindowReset(previous, updates, now)
		}
		if decision.Detected {
			var err error
			result, users, err = s.resetCarpoolSubscriptionQuotas(txCtx, accountID, CarpoolResetSourceOfficial7D, decision.EventKey)
			if err != nil {
				return err
			}
		}
		next := nextCarpoolQuotaResetState(previous, updates, decision, observedAt)
		encoded, err := json.Marshal(map[string]any{carpoolQuotaResetStateExtraKey: next})
		if err != nil {
			return err
		}
		_, err = client.ExecContext(txCtx, `
UPDATE accounts SET extra=COALESCE(extra, '{}'::jsonb) || $2::jsonb, updated_at=NOW()
WHERE id=$1`, accountID, string(encoded))
		return err
	})
	if err != nil {
		return nil, err
	}
	if result != nil && !result.AlreadyApplied && result.AffectedCount > 0 {
		s.invalidateCarpoolSubscriptionCaches(ctx, users)
		slog.Info("carpool_subscription_quota_reset_detected", "account_id", accountID,
			"event_key", decision.EventKey, "previous_used_percent", previousUsed,
			"current_used_percent", readOpenAIQuotaUsedPercent(updates, "7d"), "affected_count", result.AffectedCount)
	} else if decision.Pending != nil {
		slog.Info("carpool_7d_forced_reset_pending_confirmation", "account_id", accountID,
			"event_key", decision.EventKey, "previous_used_percent", previousUsed,
			"current_used_percent", readOpenAIQuotaUsedPercent(updates, "7d"))
	} else if skippedManualReset {
		slog.Info("carpool_quota_baseline_updated_after_manual_reset", "account_id", accountID)
	}
	return result, nil
}

// ObserveCarpoolQuotaUsage is shared by the admin quota refresh and the scanner.
func (s *SubscriptionService) ObserveCarpoolQuotaUsage(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage, observedAt time.Time) (*CarpoolQuotaResetResult, error) {
	updates := buildOpenAIAutoResetUsageUpdates(usage, observedAt)
	if len(updates) > 0 {
		updates["codex_usage_updated_at"] = observedAt.UTC().Format(time.RFC3339Nano)
	}
	return s.ObserveCarpoolQuotaSnapshot(ctx, accountID, updates, time.Now())
}
