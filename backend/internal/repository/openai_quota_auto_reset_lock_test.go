package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAutoResetDatabaseLockLifecycle(t *testing.T) {
	key := advisoryLockHash("openai_auto_reset_credit:700")
	for _, scenario := range []string{"release after cancellation", "busy", "acquire error", "unlock error"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			query := mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).WithArgs(key)
			if scenario == "acquire error" {
				query.WillReturnError(errors.New("connection lost"))
				mock.ExpectClose()
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(scenario != "busy"))
			}
			repo := &idempotencyRepository{sql: db}
			release, acquired, err := repo.TryLockOpenAIAutoResetCredit(ctx, 700)
			if scenario == "acquire error" {
				require.Error(t, err)
				require.False(t, acquired)
				require.Nil(t, release)
			} else if scenario == "busy" {
				require.NoError(t, err)
				require.False(t, acquired)
				require.Nil(t, release)
			} else {
				require.NoError(t, err)
				require.True(t, acquired)
				require.Equal(t, 1, db.Stats().InUse)
				cancel()
				unlock := mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).WithArgs(key)
				if scenario == "unlock error" {
					unlock.WillReturnError(errors.New("unlock failed"))
					mock.ExpectClose()
				} else {
					unlock.WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
				}
				release()
			}
			require.Zero(t, db.Stats().InUse)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
