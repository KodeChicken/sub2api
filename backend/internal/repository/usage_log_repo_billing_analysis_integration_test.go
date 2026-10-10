//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBillingAnalysisExcludesImagesFromRealRatio(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "billing-ratio@test.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-billing-ratio", Name: "billing-ratio"})
	account := mustCreateAccount(t, client, &service.Account{Name: "billing-ratio"})
	now := time.Now().UTC()
	tokenMode, imageMode := "token", "image"
	tokenReference, imageReference := 2.0, 0.5
	logs := []service.UsageLog{
		{BillingMode: &tokenMode, ActualCost: 1, TotalCost: 3, OfficialReferenceCost: &tokenReference},
		{BillingMode: &imageMode, ActualCost: 0.05, TotalCost: 0.1},
		{BillingMode: &tokenMode, ImageCount: 1, ActualCost: 0.2, TotalCost: 0.4, OfficialReferenceCost: &imageReference},
		{ImageCount: 1, ActualCost: 0.1, TotalCost: 0.2, OfficialReferenceCost: &imageReference},
		{ActualCost: 0.3, TotalCost: 0.6},
	}
	for i := range logs {
		logs[i].UserID, logs[i].APIKeyID, logs[i].AccountID = user.ID, apiKey.ID, account.ID
		logs[i].Model, logs[i].CreatedAt = "gpt-5.6-sol", now
		_, err := repo.Create(ctx, &logs[i])
		require.NoError(t, err)
	}

	filters := usagestats.UsageLogFilters{UserID: user.ID}
	analysis, err := repo.GetBillingAnalysis(ctx, filters)
	require.NoError(t, err)
	require.Len(t, analysis.Models, 1)
	for _, row := range []usagestats.BillingAnalysisRow{analysis.Total, analysis.Models[0]} {
		require.Equal(t, int64(5), row.Requests)
		require.Equal(t, int64(3), row.PricedRequests)
		require.InDelta(t, 1.65, row.UserCost, 1e-9)
		require.InDelta(t, 4.3, row.AccountCost, 1e-9)
		require.InDelta(t, 3, row.OfficialReferenceCost, 1e-9)
		require.Equal(t, int64(2), row.NonImageRequests)
		require.Equal(t, int64(1), row.NonImagePricedRequests)
		require.InDelta(t, 1.3, row.NonImageUserCost, 1e-9)
		require.InDelta(t, 2, row.NonImageOfficialReferenceCost, 1e-9)
	}

	filters.Model = "gpt-5.6-sol"
	users, err := repo.GetBillingAnalysisUsers(ctx, filters, 1, 50)
	require.NoError(t, err)
	require.Len(t, users.Users, 1)
	row := users.Users[0]
	require.Equal(t, user.ID, row.UserID)
	require.Equal(t, int64(5), row.Requests)
	require.InDelta(t, 1.65, row.UserCost, 1e-9)
	require.InDelta(t, 4.3, row.AccountCost, 1e-9)
	require.Equal(t, int64(2), row.NonImageRequests)
	require.Equal(t, int64(1), row.NonImagePricedRequests)
	require.InDelta(t, 1.3, row.NonImageUserCost, 1e-9)
	require.InDelta(t, 2, row.NonImageOfficialReferenceCost, 1e-9)
}
