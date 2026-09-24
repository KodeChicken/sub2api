package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyMonthlyCostUSD(t *testing.T) {
	account := &Account{Extra: map[string]any{"codex_7d_used_percent": 35.0}}
	cost := 17.0
	require.NoError(t, applyMonthlyCostUSD(account, &cost))
	require.Equal(t, 17.0, account.Extra["monthly_cost_usd"])
	require.Equal(t, 35.0, account.Extra["codex_7d_used_percent"])

	cost = 0
	require.NoError(t, applyMonthlyCostUSD(account, &cost))
	require.NotContains(t, account.Extra, "monthly_cost_usd")
	require.Equal(t, 35.0, account.Extra["codex_7d_used_percent"])

	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1)} {
		require.Error(t, applyMonthlyCostUSD(account, &invalid))
		require.NotContains(t, account.Extra, "monthly_cost_usd")
	}
}
