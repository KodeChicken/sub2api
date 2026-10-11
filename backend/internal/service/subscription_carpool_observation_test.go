package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestObserveCarpoolQuotaSnapshotRollsBackBeforeRetry(t *testing.T) {
	for _, failure := range []string{"baseline write", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			svc := NewSubscriptionService(nil, nil, nil, client, nil)
			t.Cleanup(svc.Stop)
			now := time.Now().UTC()
			baselineAt := now.Add(-time.Hour)
			newReset := now.Add(7 * 24 * time.Hour)
			// The display was overwritten by traffic before detection ran.
			extra, err := json.Marshal(map[string]any{
				"codex_7d_used_percent":  0.0,
				"codex_usage_updated_at": now.Format(time.RFC3339Nano),
				carpoolQuotaResetStateExtraKey: &carpoolQuotaResetState{
					Baseline: map[string]any{
						"codex_7d_used_percent":  80.0,
						"codex_7d_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
						"codex_usage_updated_at": baselineAt.Format(time.RFC3339Nano),
					},
					CheckedAt: baselineAt.Format(time.RFC3339Nano),
				},
			})
			require.NoError(t, err)
			updates := map[string]any{
				"codex_7d_used_percent":  0.0,
				"codex_7d_reset_at":      newReset.Format(time.RFC3339),
				"codex_usage_updated_at": now.Format(time.RFC3339Nano),
			}
			expectReset := func() {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(extra, '{}'::jsonb) FROM accounts")).
					WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"extra"}).AddRow(extra))
				mock.ExpectQuery(regexp.QuoteMeta(carpoolSubscriptionCountSQL)).
					WithArgs(int64(1), now).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
				mock.ExpectQuery("SELECT MAX\\(created_at\\)").
					WithArgs(int64(1), CarpoolResetSourceManualCard, CarpoolResetSourceAutoCard, CarpoolResetSourceFallback, baselineAt).
					WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(nil))
				mock.ExpectQuery("SELECT id FROM accounts.*FOR UPDATE").
					WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
				mock.ExpectQuery("SELECT us.id, us.user_id, us.group_id").
					WithArgs(int64(1), sqlmock.AnyArg()).
					WillReturnRows(sqlmock.NewRows([]string{
						"id", "user_id", "group_id", "daily", "weekly", "monthly",
						"daily_start", "weekly_start", "monthly_start", "revision", "reset_daily", "reset_weekly", "reset_monthly",
					}).AddRow(2, 3, 4, 3.0, 20.0, 40.0, nil, baselineAt, nil, 0, false, true, false))
				mock.ExpectQuery("INSERT INTO carpool_subscription_quota_reset_events").
					WithArgs(int64(1), CarpoolResetSourceOfficial7D, "7d:"+newReset.Format(time.RFC3339)).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5))
				mock.ExpectQuery("UPDATE user_subscriptions").
					WithArgs(int64(2), false, true, false, sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnRows(sqlmock.NewRows([]string{"quota_reset_revision"}).AddRow(1))
				mock.ExpectExec("UPDATE carpool_subscription_quota_reset_events").
					WithArgs(int64(5), 1, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			expectReset()
			injected := errors.New("injected transaction failure")
			if failure == "baseline write" {
				mock.ExpectExec("UPDATE accounts SET extra=").
					WithArgs(int64(1), sqlmock.AnyArg()).WillReturnError(injected)
				mock.ExpectRollback()
			} else {
				mock.ExpectExec("UPDATE accounts SET extra=").
					WithArgs(int64(1), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit().WillReturnError(injected)
			}
			result, err := svc.ObserveCarpoolQuotaSnapshot(context.Background(), 1, updates, now)
			require.ErrorIs(t, err, injected)
			require.Nil(t, result, "failed transactions must not report a successful reset")
			require.NoError(t, mock.ExpectationsWereMet())

			expectReset()
			mock.ExpectExec("UPDATE accounts SET extra=").
				WithArgs(int64(1), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			result, err = svc.ObserveCarpoolQuotaSnapshot(context.Background(), 1, updates, now)
			require.NoError(t, err)
			require.Equal(t, 1, result.AffectedCount)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestObserveCarpoolQuotaSnapshotSkipsOldAndManualResetObservations(t *testing.T) {
	now := time.Now().UTC()
	baselineAt := now.Add(-time.Hour)
	for _, tc := range []struct {
		name       string
		observedAt time.Time
		checkEvent bool
		writeState bool
	}{
		{"older observation", baselineAt, false, false},
		{"in flight before manual reset", now.Add(-time.Minute), true, false},
		{"rebase after manual reset", now.Add(time.Minute), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			svc := NewSubscriptionService(nil, nil, nil, client, nil)
			t.Cleanup(svc.Stop)
			baseline := map[string]any{
				"codex_7d_used_percent":  80.0,
				"codex_7d_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": baselineAt.Format(time.RFC3339Nano),
			}
			extra, err := json.Marshal(map[string]any{
				carpoolQuotaResetStateExtraKey: &carpoolQuotaResetState{Baseline: baseline, CheckedAt: baselineAt.Format(time.RFC3339Nano)},
			})
			require.NoError(t, err)
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(extra, '{}'::jsonb) FROM accounts") + ".*FOR UPDATE").
				WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"extra"}).AddRow(extra))
			if tc.checkEvent {
				mock.ExpectQuery(regexp.QuoteMeta(carpoolSubscriptionCountSQL)).
					WithArgs(int64(1), now).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
				mock.ExpectQuery("SELECT MAX\\(created_at\\)").
					WithArgs(int64(1), CarpoolResetSourceManualCard, CarpoolResetSourceAutoCard, CarpoolResetSourceFallback, baselineAt).
					WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
			}
			if tc.writeState {
				mock.ExpectExec("UPDATE accounts SET extra=").
					WithArgs(int64(1), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			mock.ExpectCommit()
			result, err := svc.ObserveCarpoolQuotaSnapshot(context.Background(), 1, map[string]any{
				"codex_7d_used_percent":  0.0,
				"codex_7d_reset_at":      now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
				"codex_usage_updated_at": tc.observedAt.Format(time.RFC3339Nano),
			}, now)
			require.NoError(t, err)
			require.Nil(t, result)
			require.NoError(t, mock.ExpectationsWereMet(), "old responses and manual resets must never reset subscriptions again")
		})
	}
}
