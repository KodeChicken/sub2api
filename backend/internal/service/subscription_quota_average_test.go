package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionQuotaPercentage(t *testing.T) {
	daily, weekly := 10.0, 20.0
	percentage, ok := subscriptionQuotaPercentage(UserSubscription{
		DailyUsageUSD:  5,
		WeeklyUsageUSD: 15,
	}, &daily, &weekly, nil)
	require.True(t, ok)
	require.InDelta(t, 62.5, percentage, 0.001)

	percentage, ok = subscriptionQuotaPercentage(UserSubscription{}, nil, nil, nil)
	require.False(t, ok)
	require.Zero(t, percentage)

	zero := 0.0
	_, ok = subscriptionQuotaPercentage(UserSubscription{}, &zero, nil, nil)
	require.False(t, ok)
}
