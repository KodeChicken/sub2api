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
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "model", "requests", "priced_requests", "user_cost", "account_cost", "official_reference_cost",
			"non_image_requests", "non_image_priced_requests", "non_image_user_cost", "non_image_official_reference_cost"}).
			AddRow(1, nil, 119, 119, 25.719620, 160.604579, 80.3022895, 119, 119, 25.719620, 80.3022895).
			AddRow(0, "gpt-6-astra", 119, 119, 25.719620, 160.604579, 80.3022895, 119, 119, 25.719620, 80.3022895))

	result, err := repo.GetBillingAnalysis(context.Background(), filters)
	require.NoError(t, err)
	require.Equal(t, int64(119), result.Total.Requests)
	require.Equal(t, int64(119), result.Total.PricedRequests)
	require.Equal(t, int64(119), result.Total.NonImageRequests)
	require.Equal(t, int64(119), result.Total.NonImagePricedRequests)
	require.Equal(t, result.Total.UserCost, result.Total.NonImageUserCost)
	require.Equal(t, result.Total.OfficialReferenceCost, result.Total.NonImageOfficialReferenceCost)
	require.InDelta(t, 0.320285, result.Total.UserCost/result.Total.OfficialReferenceCost, 0.000001)
	require.Equal(t, result.Total.UserCost, result.Models[0].UserCost)
	require.Equal(t, result.Total.AccountCost, result.Models[0].AccountCost)
	require.Equal(t, "gpt-6-astra", result.Models[0].Model)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisEmptyRange(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("GROUP BY GROUPING SETS").
		WillReturnRows(sqlmock.NewRows([]string{"model_grouped", "model", "requests", "priced_requests", "user_cost", "account_cost", "official_reference_cost",
			"non_image_requests", "non_image_priced_requests", "non_image_user_cost", "non_image_official_reference_cost"}).
			AddRow(1, nil, 0, 0, 0, 0, 0, 0, 0, 0, 0))

	result, err := repo.GetBillingAnalysis(context.Background(), usagestats.UsageLogFilters{})
	require.NoError(t, err)
	require.Zero(t, result.Total.AccountCost)
	require.Zero(t, result.Total.NonImageRequests)
	require.Zero(t, result.Total.NonImageOfficialReferenceCost)
	require.Empty(t, result.Models)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisUsersUsesPersistedCostsAndUserIdentity(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	filters := usagestats.UsageLogFilters{
		GroupID: 7, Model: "gpt-6-astra", ModelFilterSource: usagestats.ModelSourceRequested,
		StartTime: &start, EndTime: &end,
	}
	mock.ExpectQuery("SELECT g.user_id, COALESCE\\(u.username, ''\\), COALESCE\\(u.email, ''\\), g.requests, g.priced_requests, g.user_cost, g.account_cost, g.official_reference_cost").
		WithArgs(int64(7), "gpt-6-astra", start, end, 3, 2).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "requests", "priced_requests", "user_cost", "account_cost", "official_reference_cost",
			"non_image_requests", "non_image_priced_requests", "non_image_user_cost", "non_image_official_reference_cost"}).
			AddRow(10, "alice", "alice@example.com", 2, 2, 5.25, 20.5, 17.5, 2, 2, 5.25, 17.5).
			AddRow(11, "", "1173379996@qq.com", 1, 1, 0.75, 4.5, 2.5, 1, 1, 0.75, 2.5).
			AddRow(12, "bob", "bob@example.com", 1, 1, 0.25, 1.5, 1.0, 1, 1, 0.25, 1.0))
	result, err := repo.GetBillingAnalysisUsers(context.Background(), filters, 2, 2)
	require.NoError(t, err)
	require.Equal(t, []usagestats.BillingAnalysisUserRow{
		{UserID: 10, Username: "alice", Email: "alice@example.com", Requests: 2, PricedRequests: 2, UserCost: 5.25, AccountCost: 20.5, OfficialReferenceCost: 17.5,
			NonImageRequests: 2, NonImagePricedRequests: 2, NonImageUserCost: 5.25, NonImageOfficialReferenceCost: 17.5},
		{UserID: 11, Username: "", Email: "1173379996@qq.com", Requests: 1, PricedRequests: 1, UserCost: 0.75, AccountCost: 4.5, OfficialReferenceCost: 2.5,
			NonImageRequests: 1, NonImagePricedRequests: 1, NonImageUserCost: 0.75, NonImageOfficialReferenceCost: 2.5},
	}, result.Users)
	require.True(t, result.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBillingAnalysisUsersFiltersUnknownModel(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery("COALESCE\\(NULLIF\\(TRIM\\(requested_model\\), ''\\), model\\) = ''").
		WithArgs(51, 0).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "requests", "priced_requests", "user_cost", "account_cost", "official_reference_cost",
			"non_image_requests", "non_image_priced_requests", "non_image_user_cost", "non_image_official_reference_cost"}))
	result, err := repo.GetBillingAnalysisUsers(context.Background(), usagestats.UsageLogFilters{}, 1, 50)
	require.NoError(t, err)
	require.Empty(t, result.Users)
	require.False(t, result.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())
}
