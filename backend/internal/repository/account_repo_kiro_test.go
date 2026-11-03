//go:build unit

package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
