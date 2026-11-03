//go:build unit

package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestKiroRefreshCASIsScopedAndPublishesOutbox(t *testing.T) {
	for _, failure := range []bool{false, true} {
		for _, affected := range []int64{0, 1} {
			t.Run(strings.Join([]string{map[bool]string{false: "success", true: "failure"}[failure],
				map[int64]string{0: "conflict", 1: "applied"}[affected]}, "/"), func(t *testing.T) {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				defer db.Close()
				repo := &accountRepository{sql: db}
				before, after := map[string]any{"refresh_token": "old"}, map[string]any{"refresh_token": "new"}
				proxy := int64(7)
				expect := mock.ExpectExec(`(?s)WITH updated AS .*a\.platform = \$[0-9]+.*a\.credentials = \$[0-9]+::jsonb.*a\.proxy_id IS NOT DISTINCT FROM.*INSERT INTO scheduler_outbox`)
				if failure {
					expect.WithArgs(nil, "reauthorize", int64(42), service.PlatformKiro, service.AccountTypeOAuth, service.StatusActive,
						`{"refresh_token":"old"}`, proxy, service.SchedulerOutboxEventAccountChanged)
				} else {
					expect.WithArgs(`{"refresh_token":"new"}`, int64(42), service.PlatformKiro, service.AccountTypeOAuth, service.StatusActive,
						`{"refresh_token":"old"}`, proxy, service.SchedulerOutboxEventAccountChanged)
				}
				expect.WillReturnResult(sqlmock.NewResult(0, affected))
				var applied bool
				if failure {
					applied, err = repo.SetKiroOAuthRefreshFailureIfUnchanged(context.Background(), 42, before, &proxy, nil, "reauthorize")
				} else {
					applied, err = repo.UpdateKiroOAuthCredentialsIfUnchanged(context.Background(), 42, before, &proxy, after)
				}
				require.NoError(t, err)
				require.Equal(t, affected > 0, applied)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestKiroRefreshCASPropagatesDatabaseFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &accountRepository{sql: db}
	mock.ExpectExec("WITH updated AS").WillReturnError(errors.New("db down"))
	until := time.Now().Add(time.Minute)
	applied, err := repo.SetKiroOAuthRefreshFailureIfUnchanged(context.Background(), 1, nil, nil, &until, "cooldown")
	require.False(t, applied)
	require.ErrorContains(t, err, "db down")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiroMixedEmptyUnbindPublishesOldGroupsOnlyAfterCommit(t *testing.T) {
	for _, failCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "failed"}[failCommit], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			repo := &accountRepository{client: client, sql: db}
			mock.ExpectQuery(`SELECT .* FROM "account_groups"`).
				WithArgs(int64(42)).
				WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority", "created_at"}).
					AddRow(42, 10, 1, time.Now()))
			mock.ExpectBegin()
			mock.ExpectExec(`DELETE FROM "account_groups"`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			if failCommit {
				mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
			} else {
				mock.ExpectCommit()
				mock.ExpectExec("INSERT INTO scheduler_outbox").
					WithArgs(service.SchedulerOutboxEventAccountGroupsChanged, int64(42), nil, []byte(`{"group_ids":[10]}`)).
					WillReturnResult(sqlmock.NewResult(1, 1))
			}
			err = repo.BindGroups(context.Background(), 42, []int64{})
			if failCommit {
				require.ErrorContains(t, err, "commit failed")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestKiroMixedNonemptyBindKeepsExistingSuccessPath(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repo := &accountRepository{client: client, sql: db}
	mock.ExpectQuery(`SELECT .* FROM "account_groups"`).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "group_id", "priority", "created_at"}).AddRow(42, 9, 1, time.Now()))
	mock.ExpectBegin()
	mock.ExpectQuery("account_group_live_group_lock").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	mock.ExpectExec(`DELETE FROM "account_groups"`).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO "account_groups"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountGroupsChanged, int64(42), nil, []byte(`{"group_ids":[9,10]}`)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.BindGroups(context.Background(), 42, []int64{10}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKiroOAuthReauthUsesTransactionScopedCASAndOutbox(t *testing.T) {
	repo := &accountRepository{}
	applied, err := repo.ApplyKiroOAuthReauthorization(context.Background(), 42, nil, nil, nil)
	require.False(t, applied)
	require.ErrorContains(t, err, "transaction")
	for _, affected := range []int64{0, 1} {
		t.Run(map[int64]string{0: "conflict", 1: "applied"}[affected], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			mock.ExpectBegin()
			tx, err := client.Tx(context.Background())
			require.NoError(t, err)
			mock.ExpectExec(`(?s)WITH updated AS .*status=CASE WHEN a.status='error' THEN 'active' ELSE a.status END.*extra=COALESCE.*kiro_profile_checked_at.*kiro_profile_credentials_hash.*kiro_profile_arn.*a.platform=\$3 AND a.type=\$4.*a.credentials=\$5::jsonb AND a.proxy_id IS NOT DISTINCT FROM \$6.*INSERT INTO scheduler_outbox`).
				WithArgs(`{"refresh_token":"new"}`, int64(42), service.PlatformKiro, service.AccountTypeOAuth,
					`{"refresh_token":"old"}`, nil, service.SchedulerOutboxEventAccountChanged).
				WillReturnResult(sqlmock.NewResult(0, affected))
			applied, err := repo.ApplyKiroOAuthReauthorization(dbent.NewTxContext(context.Background(), tx), 42,
				map[string]any{"refresh_token": "old"}, nil, map[string]any{"refresh_token": "new"})
			require.NoError(t, err)
			require.Equal(t, affected > 0, applied)
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
