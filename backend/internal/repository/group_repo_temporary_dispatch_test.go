//go:build unit

package repository

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newTemporaryDispatchRepoTest(t *testing.T) (*groupRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	})
	return newGroupRepositoryWithSQL(nil, db), mock
}

func TestCreateTemporaryDispatchRequiresWholeBatchBeforeInstallingOverlays(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	baseline, target, current := 35.0, 65.0, 35.0
	resetAt := now.Add(5 * time.Hour)
	expiresAt := now.Add(2 * time.Hour)

	mock.ExpectQuery(regexp.QuoteMeta("WITH requested AS (")).
		WithArgs(
			"td_atomic", int64(42), service.TemporaryDispatchModeHybrid, service.TemporaryDispatchQuotaWindow5h,
			baseline, target, current, resetAt, expiresAt, now, `{7,8}`, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)).AddRow(int64(8)))

	err := repo.CreateTemporaryDispatch(context.Background(), service.TemporaryDispatchCreateSpec{
		DispatchID: "td_atomic", GroupIDs: []int64{7, 8}, AccountID: 42,
		Mode: service.TemporaryDispatchModeHybrid, QuotaWindow: service.TemporaryDispatchQuotaWindow5h,
		BaselinePercent: &baseline, TargetPercent: &target, CurrentPercent: &current,
		QuotaResetAt: &resetAt, StartedAt: now, ExpiresAt: expiresAt,
	})

	require.NoError(t, err)
}

func TestCreateTemporaryDispatchRejectsPartialBatchResult(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	now := time.Now().UTC().Truncate(time.Second)

	mock.ExpectQuery(regexp.QuoteMeta("WITH requested AS (")).
		WithArgs("td_conflict", int64(42), service.TemporaryDispatchModeTime, nil, nil, nil, nil, nil, now.Add(time.Hour), now, `{7,8}`, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))

	err := repo.CreateTemporaryDispatch(context.Background(), service.TemporaryDispatchCreateSpec{
		DispatchID: "td_conflict", GroupIDs: []int64{7, 8}, AccountID: 42,
		Mode: service.TemporaryDispatchModeTime, StartedAt: now, ExpiresAt: now.Add(time.Hour),
	})

	require.ErrorIs(t, err, service.ErrTemporaryDispatchConflict)
}

func TestTemporaryDispatchSQLGuardsCleanupByDispatchIdentity(t *testing.T) {
	data, err := os.ReadFile("group_repo.go")
	require.NoError(t, err)
	source := string(data)

	require.Contains(t, source, "temporary_dispatch_account_id = NULL")
	require.Contains(t, source, "temporary_dispatch_account_ids = '[]'::jsonb")
	require.Contains(t, source, "temporary_dispatch_account_deadlines = '{}'::jsonb")
	require.Contains(t, source, "temporary_dispatch_quota_reset_at = NULL")
	require.Contains(t, source, "WHERE (SELECT COUNT(*) FROM eligible) = (SELECT COUNT(*) FROM requested)")
	require.Contains(t, source, "FROM eligible e, inserted i")
	require.Contains(t, source, "c.account_id = m.account_id")
	require.Contains(t, source, "created_at <= $4 AND expires_at > $4")
	require.Contains(t, source, "AND NOT (g.id = ANY($1))")
	require.Contains(t, source, "WHERE account_id = $1")
	require.Contains(t, source, "a.schedulable IS TRUE")
	require.Contains(t, source, "a.rate_limit_reset_at IS NULL OR a.rate_limit_reset_at <= $1")
	require.Contains(t, source, "t.last_checked_at IS NULL OR t.last_checked_at <= $1")
}

func TestStopTemporaryDispatchGroupsReturnsOnlyClearedGroups(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.dispatch_id")).
		WithArgs(`{7}`).
		WillReturnRows(sqlmock.NewRows([]string{"dispatch_id"}).AddRow("td_multi"))
	mock.ExpectQuery(regexp.QuoteMeta("WITH selected_tasks AS MATERIALIZED (")).
		WithArgs(`{7}`, `{"td_multi"}`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	ids, err := repo.StopTemporaryDispatchGroups(context.Background(), []int64{7})

	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
}

func TestObserveTemporaryDispatchQuotaReturnsOnlyCompletedDispatchGroups(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.dispatch_id")).
		WithArgs(int64(42), service.TemporaryDispatchQuotaWindow5h, now).
		WillReturnRows(sqlmock.NewRows([]string{"dispatch_id"}).AddRow("td_multi"))
	mock.ExpectQuery(regexp.QuoteMeta("WITH matched AS MATERIALIZED (")).
		WithArgs(int64(42), service.TemporaryDispatchQuotaWindow5h, 65.0, now, `{"td_multi"}`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)).AddRow(int64(8)))
	mock.ExpectCommit()

	ids, err := repo.ObserveTemporaryDispatchQuota(context.Background(), 42, service.TemporaryDispatchQuotaWindow5h, 65, now)

	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, ids)
}

func TestStopTemporaryDispatchAccountRemovesOnlyMatchingPoolMember(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.dispatch_id")).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"dispatch_id"}).AddRow("td_multi"))
	mock.ExpectQuery(regexp.QuoteMeta("WITH selected_members AS MATERIALIZED (")).
		WithArgs(int64(42), `{"td_multi"}`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)).AddRow(int64(8)))
	mock.ExpectCommit()

	ids, err := repo.StopTemporaryDispatchAccount(context.Background(), 42)

	require.NoError(t, err)
	require.Equal(t, []int64{7, 8}, ids)
}

func TestObserveTemporaryDispatchQuotaSkipsMutationWhenNoTaskCanBeLocked(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.dispatch_id")).
		WithArgs(int64(42), service.TemporaryDispatchQuotaWindow5h, now).
		WillReturnRows(sqlmock.NewRows([]string{"dispatch_id"}))
	mock.ExpectCommit()

	ids, err := repo.ObserveTemporaryDispatchQuota(context.Background(), 42, service.TemporaryDispatchQuotaWindow5h, 65, now)

	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestCleanupTemporaryDispatchesStillClearsOrphansWithoutLockedTasks(t *testing.T) {
	repo, mock := newTemporaryDispatchRepoTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("WITH candidates AS MATERIALIZED (")).
		WithArgs(now, 200).
		WillReturnRows(sqlmock.NewRows([]string{"dispatch_id"}))
	mock.ExpectQuery(regexp.QuoteMeta("WITH due_members AS MATERIALIZED (")).
		WithArgs(now, 200, `{}`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectCommit()

	ids, err := repo.CleanupTemporaryDispatches(context.Background(), now, 0)

	require.NoError(t, err)
	require.Equal(t, []int64{7}, ids)
}
