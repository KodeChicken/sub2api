package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestBillingAnalysisSeparatesPersistedCostsForAccountFilter(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	filters := usagestats.UsageLogFilters{
		AccountID: 42, Model: "gpt-6-astra", ModelFilterSource: usagestats.ModelSourceRequested,
		StartTime: &start, EndTime: &end,
	}
	mock.ExpectQuery("GROUP BY GROUPING SETS").
		WithArgs(int64(42), "gpt-6-astra", start, end).
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "model", "requests", "user_cost", "account_cost"}).
			AddRow(1, nil, 119, 25.719620, 160.604579).
			AddRow(0, "gpt-6-astra", 119, 25.719620, 160.604579))

	result, err := repo.GetBillingAnalysis(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(119), result.Total.Requests)
	require.InDelta(t, 0.1601, result.Total.UserCost/result.Total.AccountCost, 0.0001)
	require.Equal(t, result.Total.UserCost, result.Models[0].UserCost)
	require.Equal(t, result.Total.AccountCost, result.Models[0].AccountCost)
	require.Equal(t, "gpt-6-astra", result.Models[0].Model)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisEmptyRange(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("GROUP BY GROUPING SETS").
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "model", "requests", "user_cost", "account_cost"}).
			AddRow(1, nil, 0, 0, 0))

	result, err := repo.GetBillingAnalysis(context.Background(), usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.Zero(t, result.Total.AccountCost)
	require.Empty(t, result.Models)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisUsersUsesPersistedCostsAndUsername(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	filters := usagestats.UsageLogFilters{
		GroupID: 7, Model: "gpt-6-astra", ModelFilterSource: usagestats.ModelSourceRequested,
		StartTime: &start, EndTime: &end,
	}
	mock.ExpectQuery("LEFT JOIN users u ON u.id = g.user_id").
		WithArgs(int64(7), "gpt-6-astra", start, end, 3, 2).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "requests", "user_cost", "account_cost"}).
			AddRow(10, "alice", 2, 5.25, 20.5).
			AddRow(11, "", 1, 0.75, 4.5).
			AddRow(12, "bob", 1, 0.25, 1.5))
	result, err := repo.GetBillingAnalysisUsers(context.Background(), filters, 2, 2)
	require.NoError(t, err)
	require.Equal(t, []usagestats.BillingAnalysisUserRow{
		{UserID: 10, Username: "alice", Requests: 2, UserCost: 5.25, AccountCost: 20.5},
		{UserID: 11, Username: "", Requests: 1, UserCost: 0.75, AccountCost: 4.5},
	}, result.Users)
	require.True(t, result.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisUsersFiltersUnknownModel(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("COALESCE\\(NULLIF\\(TRIM\\(requested_model\\), ''\\), model\\) = ''").
		WithArgs(51, 0).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "requests", "user_cost", "account_cost"}))
	result, err := repo.GetBillingAnalysisUsers(context.Background(), usagestats.UsageLogFilters{}, 1, 50)
	require.NoError(t, err)
	require.Empty(t, result.Users)
	require.False(t, result.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())
}
