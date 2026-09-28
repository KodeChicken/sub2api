package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestCalculateOfficialReferenceCostGPT6Sol(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	cost := &CostBreakdown{
		BillingMode:  string(BillingModeToken),
		BillingModel: "gpt-6-sol",
	}

	referenceCost := calculateOfficialReferenceCost(
		billingService,
		cost,
		UsageTokens{InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024},
		"",
		"",
		time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	)

	require.NotNil(t, referenceCost)
	require.InDelta(t, 0.0428188, *referenceCost, 1e-12)
}

func TestCalculateOfficialReferenceCostExcludesUnrecoverableSurcharge(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	cost := &CostBreakdown{
		InputCost:    0.002,
		TotalCost:    0.012,
		BillingMode:  string(BillingModeToken),
		BillingModel: "gpt-6-sol",
	}

	require.Nil(t, calculateOfficialReferenceCost(
		billingService,
		cost,
		UsageTokens{InputTokens: 1000},
		"",
		"",
		time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	))
}

func TestApplyBillingAnalysisReferenceCostsUsesPerRequestEvidence(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	persistedReference := 0.04
	tokenMode := string(BillingModeToken)
	ambiguousUpstream := "gpt-6-luna"
	logs := []UsageLog{
		{
			UserID: 1, Model: "gpt-6-sol", RequestedModel: "gpt-6-sol",
			OfficialReferenceCost: &persistedReference,
		},
		{
			UserID: 1, Model: "gpt-6-sol", RequestedModel: "gpt-6-sol", BillingMode: &tokenMode,
			InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024,
			CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			UserID: 1, Model: "gpt-6-sol", RequestedModel: "gpt-6-sol", UpstreamModel: &ambiguousUpstream,
			BillingMode: &tokenMode, InputTokens: 1000,
			CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
	}
	analysis := &usagestats.BillingAnalysis{
		Total:  usagestats.BillingAnalysisRow{Requests: 3},
		Models: []usagestats.BillingAnalysisRow{{Model: "gpt-6-sol", Requests: 3}},
	}

	applyBillingAnalysisReferenceCosts(analysis, logs, billingService)

	require.Equal(t, int64(2), analysis.Total.PricedRequests)
	require.InDelta(t, 0.0828188, analysis.Total.OfficialReferenceCost, 1e-12)
	require.Equal(t, analysis.Total.PricedRequests, analysis.Models[0].PricedRequests)
	require.Equal(t, analysis.Total.OfficialReferenceCost, analysis.Models[0].OfficialReferenceCost)
}
