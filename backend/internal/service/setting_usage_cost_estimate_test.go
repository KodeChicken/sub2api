package service

import (
	"context"
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestUsageCostEstimateSettings(t *testing.T) {
	repo := &panelRateLimitSettingRepo{}
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()

	value, err := svc.GetUsageCostEstimate(ctx)
	require.NoError(t, err)
	require.Equal(t, &UsageCostEstimate{}, value)

	for _, invalid := range []UsageCostEstimate{
		{WeeklyCostUSD: 17},
		{WeeklyQuotaUSD: 100},
		{WeeklyCostUSD: -1, WeeklyQuotaUSD: 100},
		{WeeklyCostUSD: math.NaN(), WeeklyQuotaUSD: 100},
		{WeeklyCostUSD: 17, WeeklyQuotaUSD: math.Inf(1)},
		{WeeklyCostUSD: 1e300, WeeklyQuotaUSD: 1e-300},
		{WeeklyCostUSD: 1e-300, WeeklyQuotaUSD: 1e300},
	} {
		require.Error(t, svc.SetUsageCostEstimate(ctx, invalid))
	}

	expected := UsageCostEstimate{WeeklyCostUSD: 17, WeeklyQuotaUSD: 100}
	require.NoError(t, svc.SetUsageCostEstimate(ctx, expected))
	value, err = svc.GetUsageCostEstimate(ctx)
	require.NoError(t, err)
	require.Equal(t, &expected, value)

	require.NoError(t, svc.SetUsageCostEstimate(ctx, UsageCostEstimate{}))
	value, err = svc.GetUsageCostEstimate(ctx)
	require.NoError(t, err)
	require.Equal(t, &UsageCostEstimate{}, value)
	require.NotContains(t, repo.values, SettingKeyUsageCostEstimate)
}

func TestUsageCostEstimateRejectsCorruptStoredValue(t *testing.T) {
	repo := &panelRateLimitSettingRepo{values: map[string]string{
		SettingKeyUsageCostEstimate: `{"weekly_cost_usd":17}`,
	}}
	svc := NewSettingService(repo, &config.Config{})
	_, err := svc.GetUsageCostEstimate(context.Background())
	require.Error(t, err)
}
