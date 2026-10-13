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

func TestBillingAnalysisIncludesOnlyTokenBilledRequests(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "billing-ratio@test.com"})
	apiKey := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-billing-ratio", Name: "billing-ratio"})
	account := mustCreateAccount(t, client, &service.Account{Name: "billing-ratio"})
	now := time.Now().UTC()
	tokenMode, imageMode := "token", "image"
	perRequestMode, videoMode, emptyMode := "per_request", "video", ""
	tokenReference, imageReference := 2.0, 0.5
	logs := []service.UsageLog{
		{BillingMode: &tokenMode, ActualCost: 1, TotalCost: 3, OfficialReferenceCost: &tokenReference},
		{BillingMode: &imageMode, ActualCost: 0.05, TotalCost: 0.1},
		{BillingMode: &tokenMode, ImageCount: 1, ActualCost: 0.2, TotalCost: 0.4, OfficialReferenceCost: &imageReference},
		{ImageCount: 1, ActualCost: 0.1, TotalCost: 0.2, OfficialReferenceCost: &imageReference},
		{ActualCost: 0.3, TotalCost: 0.6},
		{BillingMode: &perRequestMode, ActualCost: 5, TotalCost: 10},
		{BillingMode: &videoMode, ActualCost: 7, TotalCost: 14},
		{BillingMode: &emptyMode, ActualCost: 0.4, TotalCost: 0.8, OfficialReferenceCost: &imageReference},
		{Model: "tool-only", BillingMode: &perRequestMode, ActualCost: 9, TotalCost: 18},
	}
	for i := range logs {
		logs[i].UserID, logs[i].APIKeyID, logs[i].AccountID = user.ID, apiKey.ID, account.ID
		if logs[i].Model == "" {
			logs[i].Model = "gpt-5.6-sol"
		}
		logs[i].CreatedAt = now
		_, err := repo.Create(ctx, &logs[i])
		require.NoError(t, err)
	}

	filters := usagestats.UsageLogFilters{UserID: user.ID}
	analysis, err := repo.GetBillingAnalysis(ctx, filters)
	require.NoError(t, err)
	require.Len(t, analysis.Models, 1)
	for _, row := range []usagestats.BillingAnalysisRow{analysis.Total, analysis.Models[0]} {
		require.Equal(t, int64(4), row.Requests)
		require.Equal(t, int64(3), row.PricedRequests)
		require.InDelta(t, 1.9, row.UserCost, 1e-9)
		require.InDelta(t, 4.8, row.AccountCost, 1e-9)
		require.InDelta(t, 3, row.OfficialReferenceCost, 1e-9)
		require.Equal(t, int64(3), row.NonImageRequests)
		require.Equal(t, int64(2), row.NonImagePricedRequests)
		require.InDelta(t, 1.7, row.NonImageUserCost, 1e-9)
		require.InDelta(t, 2.5, row.NonImageOfficialReferenceCost, 1e-9)
	}

	filters.Model = "gpt-5.6-sol"
	users, err := repo.GetBillingAnalysisUsers(ctx, filters, 1, 50)
	require.NoError(t, err)
	require.Len(t, users.Users, 1)
	row := users.Users[0]
	require.Equal(t, user.ID, row.UserID)
	require.Equal(t, analysis.Total.Requests, row.Requests)
	require.Equal(t, analysis.Total.PricedRequests, row.PricedRequests)
	require.Equal(t, analysis.Total.UserCost, row.UserCost)
	require.Equal(t, analysis.Total.AccountCost, row.AccountCost)
	require.Equal(t, analysis.Total.OfficialReferenceCost, row.OfficialReferenceCost)

	missing, err := repo.ListMissingBillingReferenceUsageLogs(ctx, filters)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	require.InDelta(t, 0.3, missing[0].ActualCost, 1e-9)

	filters.Model = "tool-only"
	users, err = repo.GetBillingAnalysisUsers(ctx, filters, 1, 50)
	require.NoError(t, err)
	require.Empty(t, users.Users)
}
