package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	CarpoolResetSourceOfficial7D = "official_7d_reset"
	CarpoolResetSourceManualCard = "manual_reset_card"
	CarpoolResetSourceAutoCard   = "automatic_reset_card"
	CarpoolResetSourceFallback   = "manual_fallback"
)

var (
	ErrCarpoolUndoUnavailable = infraerrors.Conflict("CARPOOL_RESET_UNDO_UNAVAILABLE", "no carpool subscription reset is available to undo")
	ErrCarpoolUndoConflict    = infraerrors.Conflict("CARPOOL_RESET_UNDO_CONFLICT", "a subscription quota window was reset again after this event; undo was refused to avoid overwriting newer quota state")
)

type CarpoolQuotaResetResult struct {
	EventID        int64  `json:"event_id,omitempty"`
	AccountID      int64  `json:"account_id"`
	Source         string `json:"source,omitempty"`
	AffectedCount  int    `json:"affected_count"`
	AlreadyApplied bool   `json:"already_applied,omitempty"`
}

type carpoolQuotaSnapshot struct {
	SubscriptionID int64      `json:"subscription_id"`
	UserID         int64      `json:"user_id"`
	GroupID        int64      `json:"group_id"`
	ResetDaily     bool       `json:"reset_daily"`
	ResetWeekly    bool       `json:"reset_weekly"`
	ResetMonthly   bool       `json:"reset_monthly"`
	DailyUsage     float64    `json:"daily_usage"`
	WeeklyUsage    float64    `json:"weekly_usage"`
	MonthlyUsage   float64    `json:"monthly_usage"`
	DailyWindow    *time.Time `json:"daily_window,omitempty"`
	WeeklyWindow   *time.Time `json:"weekly_window,omitempty"`
	MonthlyWindow  *time.Time `json:"monthly_window,omitempty"`
	RevisionAfter  int64      `json:"revision_after"`
}

type carpoolQuotaRow struct {
	carpoolQuotaSnapshot
	RevisionBefore int64
}

func (s *SubscriptionService) withCarpoolQuotaTx(ctx context.Context, fn func(context.Context, *dbent.Client) error) error {
	if s == nil || s.entClient == nil {
		return errors.New("subscription database is not available")
	}
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(ctx, tx.Client())
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin carpool quota transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit carpool quota transaction: %w", err)
	}
	return nil
}

// PreviewCarpoolSubscriptionQuotaReset returns the count of active subscriptions
// with at least one configured quota in carpool groups bound to this account.
func (s *SubscriptionService) PreviewCarpoolSubscriptionQuotaReset(ctx context.Context, accountID int64) (int, error) {
	if accountID <= 0 || s == nil || s.entClient == nil {
		return 0, ErrInvalidInput
	}
	var count int
	err := scanCarpoolQueryRow(ctx, s.entClient, carpoolSubscriptionCountSQL, []any{&count}, accountID, time.Now())
	return count, err
}

const carpoolSubscriptionCountSQL = `
SELECT COUNT(*)
FROM user_subscriptions us
JOIN groups g ON g.id = us.group_id
JOIN account_groups ag ON ag.group_id = g.id
WHERE ag.account_id = $1
  AND g.deleted_at IS NULL AND g.is_carpool = TRUE
  AND g.subscription_type = 'subscription'
  AND us.deleted_at IS NULL AND us.status = 'active' AND us.expires_at > $2
  AND ((g.daily_limit_usd IS NOT NULL AND g.daily_limit_usd > 0)
    OR (g.weekly_limit_usd IS NOT NULL AND g.weekly_limit_usd > 0)
    OR (g.monthly_limit_usd IS NOT NULL AND g.monthly_limit_usd > 0))`

// CarpoolAccountIDs returns accounts that currently have active subscriptions
// on an enabled carpool group. The periodic quota scanner uses this to avoid
// querying upstream quota for unrelated OpenAI accounts.
func (s *SubscriptionService) CarpoolAccountIDs(ctx context.Context) (map[int64]struct{}, error) {
	ids := make(map[int64]struct{})
	if s == nil || s.entClient == nil {
		return ids, nil
	}
	rows, err := s.entClient.QueryContext(ctx, `
SELECT DISTINCT ag.account_id
FROM account_groups ag
JOIN groups g ON g.id = ag.group_id
JOIN user_subscriptions us ON us.group_id = g.id
WHERE g.deleted_at IS NULL AND g.is_carpool = TRUE
  AND g.subscription_type = 'subscription'
  AND us.deleted_at IS NULL AND us.status = 'active' AND us.expires_at > NOW()
  AND ((g.daily_limit_usd IS NOT NULL AND g.daily_limit_usd > 0)
    OR (g.weekly_limit_usd IS NOT NULL AND g.weekly_limit_usd > 0)
    OR (g.monthly_limit_usd IS NOT NULL AND g.monthly_limit_usd > 0))`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

// CarpoolCardResetSinceSnapshot prevents a just-consumed reset card from being
// misclassified as an official 7d refresh when the next usage read observes the
// same 7d drop. The baseline timestamp is from accounts.extra.
func (s *SubscriptionService) CarpoolCardResetSinceSnapshot(ctx context.Context, accountID int64, extra map[string]any, now time.Time) (bool, error) {
	if s == nil || s.entClient == nil || accountID <= 0 {
		return false, nil
	}
	since := now.Add(-carpoolQuotaSnapshotInterval)
	if snapshotAt, ok := parseCarpoolResetTime(extra["codex_usage_updated_at"]); ok {
		since = snapshotAt
	}
	var exists bool
	err := scanCarpoolQueryRow(ctx, s.entClient, `
SELECT EXISTS (
    SELECT 1 FROM carpool_subscription_quota_reset_events
    WHERE account_id=$1
      AND source IN ($2, $3)
      AND status='applied'
      AND created_at > $4
)`, []any{&exists}, accountID, CarpoolResetSourceManualCard, CarpoolResetSourceAutoCard, since)
	return exists, err
}

// ResetCarpoolSubscriptionQuotas resets every active subscription configured on
// carpool groups bound to an account. The event and quota changes commit together.
func (s *SubscriptionService) ResetCarpoolSubscriptionQuotas(ctx context.Context, accountID int64, source, eventKey string) (*CarpoolQuotaResetResult, error) {
	if accountID <= 0 || source == "" || eventKey == "" || s == nil || s.entClient == nil {
		return nil, ErrInvalidInput
	}
	result := &CarpoolQuotaResetResult{AccountID: accountID, Source: source}
	users := make(map[int64]int64)
	now := time.Now()
	dailyStart := timezone.StartOfDay(now)
	err := s.withCarpoolQuotaTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		rows, err := client.QueryContext(txCtx, `
SELECT us.id, us.user_id, us.group_id,
       us.daily_usage_usd, us.weekly_usage_usd, us.monthly_usage_usd,
       us.daily_window_start, us.weekly_window_start, us.monthly_window_start,
       us.quota_reset_revision,
       (g.daily_limit_usd IS NOT NULL AND g.daily_limit_usd > 0),
       (g.weekly_limit_usd IS NOT NULL AND g.weekly_limit_usd > 0),
       (g.monthly_limit_usd IS NOT NULL AND g.monthly_limit_usd > 0)
FROM user_subscriptions us
JOIN groups g ON g.id = us.group_id
JOIN account_groups ag ON ag.group_id = g.id
WHERE ag.account_id = $1
  AND g.deleted_at IS NULL AND g.is_carpool = TRUE
  AND g.subscription_type = 'subscription'
  AND us.deleted_at IS NULL AND us.status = 'active' AND us.expires_at > $2
  AND ((g.daily_limit_usd IS NOT NULL AND g.daily_limit_usd > 0)
    OR (g.weekly_limit_usd IS NOT NULL AND g.weekly_limit_usd > 0)
    OR (g.monthly_limit_usd IS NOT NULL AND g.monthly_limit_usd > 0))
ORDER BY us.id
FOR UPDATE OF us`, accountID, now)
		if err != nil {
			return err
		}
		var items []carpoolQuotaRow
		for rows.Next() {
			var row carpoolQuotaRow
			var dailyWindow, weeklyWindow, monthlyWindow sql.NullTime
			if err := rows.Scan(
				&row.SubscriptionID, &row.UserID, &row.GroupID,
				&row.DailyUsage, &row.WeeklyUsage, &row.MonthlyUsage,
				&dailyWindow, &weeklyWindow, &monthlyWindow,
				&row.RevisionBefore, &row.ResetDaily, &row.ResetWeekly, &row.ResetMonthly,
			); err != nil {
				_ = rows.Close()
				return err
			}
			row.DailyWindow = nullableTimePointer(dailyWindow)
			row.WeeklyWindow = nullableTimePointer(weeklyWindow)
			row.MonthlyWindow = nullableTimePointer(monthlyWindow)
			row.RevisionAfter = row.RevisionBefore + 1
			items = append(items, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		if len(items) == 0 {
			return nil
		}

		var eventID int64
		err = scanCarpoolQueryRow(txCtx, client, `
INSERT INTO carpool_subscription_quota_reset_events
    (account_id, source, event_key, status, affected_count, snapshot)
VALUES ($1, $2, $3, 'applying', 0, '[]'::jsonb)
ON CONFLICT (account_id, source, event_key) DO NOTHING
			RETURNING id`, []any{&eventID}, accountID, source, eventKey)
		if errors.Is(err, sql.ErrNoRows) {
			var existingID int64
			var existingCount int
			var status string
			if readErr := scanCarpoolQueryRow(txCtx, client, `SELECT id, affected_count, status FROM carpool_subscription_quota_reset_events WHERE account_id=$1 AND source=$2 AND event_key=$3`, []any{&existingID, &existingCount, &status}, accountID, source, eventKey); readErr != nil {
				return readErr
			}
			result.EventID = existingID
			result.AffectedCount = existingCount
			result.AlreadyApplied = status == "applied"
			return nil
		}
		if err != nil {
			return err
		}

		for i := range items {
			row := items[i]
			var revision int64
			if err := scanCarpoolQueryRow(txCtx, client, `
UPDATE user_subscriptions
SET daily_usage_usd = CASE WHEN $2 THEN 0 ELSE daily_usage_usd END,
    weekly_usage_usd = CASE WHEN $3 THEN 0 ELSE weekly_usage_usd END,
    monthly_usage_usd = CASE WHEN $4 THEN 0 ELSE monthly_usage_usd END,
    daily_window_start = CASE WHEN $2 THEN $5 ELSE daily_window_start END,
    weekly_window_start = CASE WHEN $3 THEN $6 ELSE weekly_window_start END,
    monthly_window_start = CASE WHEN $4 THEN $6 ELSE monthly_window_start END,
    quota_reset_revision = quota_reset_revision + 1,
    updated_at = NOW()
WHERE id = $1
RETURNING quota_reset_revision`, []any{&revision}, row.SubscriptionID, row.ResetDaily, row.ResetWeekly, row.ResetMonthly, dailyStart, now); err != nil {
				return err
			}
			row.RevisionAfter = revision
			users[row.UserID] = row.GroupID
			items[i] = row
		}
		snapshots := make([]carpoolQuotaSnapshot, 0, len(items))
		for _, row := range items {
			snapshots = append(snapshots, row.carpoolQuotaSnapshot)
		}
		snapshotJSON, err := json.Marshal(snapshots)
		if err != nil {
			return err
		}
		if _, err := client.ExecContext(txCtx, `UPDATE carpool_subscription_quota_reset_events SET status='applied', affected_count=$2, snapshot=$3::jsonb WHERE id=$1`, eventID, len(items), string(snapshotJSON)); err != nil {
			return err
		}
		result.EventID = eventID
		result.AffectedCount = len(items)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !result.AlreadyApplied && result.AffectedCount > 0 {
		s.invalidateCarpoolSubscriptionCaches(ctx, users)
	}
	return result, nil
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}

func scanCarpoolQueryRow(ctx context.Context, client *dbent.Client, query string, dest []any, args ...any) error {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return rows.Scan(dest...)
}

// DetectCarpool7DWindowReset recognizes a successful 7-day window refresh from
// two account snapshots. A 5-hour window reaching zero is intentionally ignored.
func DetectCarpool7DWindowReset(previousExtra map[string]any, usage *OpenAIQuotaUsage, now time.Time) (string, bool) {
	if usage == nil || previousExtra == nil {
		return "", false
	}
	return DetectCarpool7DWindowResetFromUpdates(previousExtra, buildOpenAIAutoResetUsageUpdates(usage, now), now)
}

func DetectCarpool7DWindowResetFromUpdates(previousExtra, updates map[string]any, now time.Time) (string, bool) {
	if previousExtra == nil || updates == nil {
		return "", false
	}
	previousUsed := readOpenAIQuotaUsedPercent(previousExtra, "7d") / 100
	if previousUsed < 0.10 {
		return "", false
	}
	if _, hasCurrent7DUsage := updates["codex_7d_used_percent"]; !hasCurrent7DUsage {
		return "", false
	}
	currentUsed := readOpenAIQuotaUsedPercent(updates, "7d") / 100
	if currentUsed > 0.01 || currentUsed >= previousUsed {
		return "", false
	}
	previousReset, previousResetOK := parseCarpoolResetTime(previousExtra["codex_7d_reset_at"])
	currentReset, currentResetOK := parseCarpoolResetTime(updates["codex_7d_reset_at"])
	resetAdvanced := previousResetOK && currentResetOK && currentReset.After(previousReset.Add(6*time.Hour))
	// Exact zero is a strong signal even when an upstream response omits reset_at.
	if !resetAdvanced && currentUsed > 0 {
		return "", false
	}
	key := "7d-zero"
	if currentResetOK {
		key = "7d:" + currentReset.UTC().Format(time.RFC3339)
	} else if previousResetOK {
		key += ":" + previousReset.UTC().Format(time.RFC3339)
	}
	return key, true
}

func parseCarpoolResetTime(raw any) (time.Time, bool) {
	value, ok := raw.(string)
	if !ok || value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	return t, err == nil
}

// UndoLatestCarpoolSubscriptionQuotaReset restores the prior usage/window
// snapshot while adding usage accrued since the reset. A later reset increments
// quota_reset_revision and makes the whole undo fail atomically.
func (s *SubscriptionService) UndoLatestCarpoolSubscriptionQuotaReset(ctx context.Context, accountID int64) (*CarpoolQuotaResetResult, error) {
	if accountID <= 0 || s == nil || s.entClient == nil {
		return nil, ErrInvalidInput
	}
	result := &CarpoolQuotaResetResult{AccountID: accountID}
	users := make(map[int64]int64)
	err := s.withCarpoolQuotaTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		var eventID int64
		var source, status string
		var rawSnapshot []byte
		err := scanCarpoolQueryRow(txCtx, client, `
SELECT id, source, status, snapshot
FROM carpool_subscription_quota_reset_events
WHERE account_id=$1
ORDER BY id DESC
LIMIT 1 FOR UPDATE`, []any{&eventID, &source, &status, &rawSnapshot}, accountID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && status != "applied") {
			return ErrCarpoolUndoUnavailable
		}
		if err != nil {
			return err
		}
		var snapshots []carpoolQuotaSnapshot
		if err := json.Unmarshal(rawSnapshot, &snapshots); err != nil {
			return fmt.Errorf("decode carpool quota reset snapshot: %w", err)
		}
		for _, snapshot := range snapshots {
			res, err := client.ExecContext(txCtx, `
UPDATE user_subscriptions
SET daily_usage_usd = CASE WHEN $2 THEN daily_usage_usd + $5 ELSE daily_usage_usd END,
    weekly_usage_usd = CASE WHEN $3 THEN weekly_usage_usd + $6 ELSE weekly_usage_usd END,
    monthly_usage_usd = CASE WHEN $4 THEN monthly_usage_usd + $7 ELSE monthly_usage_usd END,
    daily_window_start = CASE WHEN $2 THEN $8 ELSE daily_window_start END,
    weekly_window_start = CASE WHEN $3 THEN $9 ELSE weekly_window_start END,
    monthly_window_start = CASE WHEN $4 THEN $10 ELSE monthly_window_start END,
    quota_reset_revision = quota_reset_revision + 1,
    updated_at = NOW()
WHERE id=$1 AND quota_reset_revision=$11`, snapshot.SubscriptionID,
				snapshot.ResetDaily, snapshot.ResetWeekly, snapshot.ResetMonthly,
				snapshot.DailyUsage, snapshot.WeeklyUsage, snapshot.MonthlyUsage,
				snapshot.DailyWindow, snapshot.WeeklyWindow, snapshot.MonthlyWindow,
				snapshot.RevisionAfter)
			if err != nil {
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrCarpoolUndoConflict
			}
			users[snapshot.UserID] = snapshot.GroupID
		}
		if _, err := client.ExecContext(txCtx, `UPDATE carpool_subscription_quota_reset_events SET status='undone', undone_at=NOW() WHERE id=$1 AND status='applied'`, eventID); err != nil {
			return err
		}
		result.EventID = eventID
		result.Source = source
		result.AffectedCount = len(snapshots)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCarpoolSubscriptionCaches(ctx, users)
	return result, nil
}

func (s *SubscriptionService) invalidateCarpoolSubscriptionCaches(ctx context.Context, users map[int64]int64) {
	for userID, groupID := range users {
		s.InvalidateSubCacheSync(userID, groupID)
		if s.billingCacheService != nil {
			_ = s.billingCacheService.InvalidateSubscription(ctx, userID, groupID)
		}
	}
}
