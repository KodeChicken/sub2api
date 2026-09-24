package repository

import (
	"context"
	"fmt"
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
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "account_grouped", "model", "account_id", "requests", "user_cost", "account_cost"}).
			AddRow(1, 1, nil, nil, 119, 25.719620, 160.604579).
			AddRow(0, 1, "gpt-6-astra", nil, 119, 25.719620, 160.604579).
			AddRow(1, 0, nil, 42, 119, 25.719620, 160.604579))
	resetAt := time.Now().Add(3 * 24 * time.Hour).UTC().Format(time.RFC3339)
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	extra := fmt.Sprintf(`{"monthly_cost_usd":17,"codex_7d_used_percent":40,"codex_7d_reset_at":%q,"codex_usage_updated_at":%q}`, resetAt, updatedAt)
	mock.ExpectQuery("SELECT id, extra FROM accounts").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "extra"}).AddRow(42, []byte(extra)))
	mock.ExpectQuery("SELECT w.id").WithArgs(int64(42), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cost"}).AddRow(42, 40.0))

	result, err := repo.GetBillingAnalysis(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(119), result.Total.Requests)
	require.InDelta(t, 0.1601, result.Total.UserCost/result.Total.AccountCost, 0.0001)
	require.Equal(t, result.Total.UserCost, result.Models[0].UserCost)
	require.Equal(t, result.Total.AccountCost, result.Models[0].AccountCost)
	require.Equal(t, "gpt-6-astra", result.Models[0].Model)
	require.Equal(t, float64(17), *result.Accounts[0].MonthlyCost)
	require.InDelta(t, 100, *result.Accounts[0].SevenDayEstimate, 0.001)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisEmptyRange(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("GROUP BY GROUPING SETS").
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "account_grouped", "model", "account_id", "requests", "user_cost", "account_cost"}).
			AddRow(1, 1, nil, nil, 0, 0, 0))

	result, err := repo.GetBillingAnalysis(context.Background(), usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.Zero(t, result.Total.AccountCost)
	require.Empty(t, result.Models)
	require.Empty(t, result.Accounts)
	require.NoError(t, mock.ExpectationsWereMet())
}
