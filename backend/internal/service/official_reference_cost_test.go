package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestCalculateOfficialReferenceCostGPT6Sol(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	referenceCost := CalculateOfficialReferenceCost(billingService, OfficialReferenceInput{
		BillingMode:  string(BillingModeToken),
		BillingModel: "gpt-6-sol",
		Tokens:       UsageTokens{InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024},
		PricingAt:    time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	})

	require.NotNil(t, referenceCost)
	require.InDelta(t, 0.0428188, referenceCost.Cost, 1e-12)
	require.False(t, referenceCost.LongContextApplied)
}

func TestCalculateOfficialReferenceCostExcludesUnrecoverableSurcharge(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	require.Nil(t, CalculateOfficialReferenceCost(billingService, OfficialReferenceInput{
		BillingMode:           string(BillingModeToken),
		BillingModel:          "gpt-6-sol",
		Tokens:                UsageTokens{InputTokens: 1000},
		PricingAt:             time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		CustomerComponentCost: 0.002,
		CustomerTotalCost:     0.012,
	}))
}

func TestCalculateOfficialReferenceCostLongContextIsIndependentFromCustomerCost(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	customerCost := &CostBreakdown{
		ActualCost:                52.076011,
		BillingMode:               string(BillingModeToken),
		BillingModel:              "gpt-6-sol",
		LongContextBillingApplied: false,
	}
	input, ok := officialReferenceInputFromCost(
		customerCost,
		UsageTokens{InputTokens: 1000, CacheReadTokens: 272000, OutputTokens: 1000},
		"",
		"",
		time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		true,
	)
	require.True(t, ok)

	result := CalculateOfficialReferenceCost(billingService, input)

	require.NotNil(t, result)
	require.True(t, result.LongContextApplied)
	require.Equal(t, 52.076011, customerCost.ActualCost)
	require.False(t, customerCost.LongContextBillingApplied)
}

func TestEffectiveOfficialReferenceLongContextEnabled(t *testing.T) {
	trueValue := true
	falseValue := false

	tests := []struct {
		name        string
		group       *Group
		accountGate *bool
		want        bool
	}{
		{name: "both off", group: &Group{}, accountGate: &falseValue, want: false},
		{name: "group on account off", group: &Group{LongContextPricingEnabled: true}, accountGate: &falseValue, want: true},
		{name: "group off account on", group: &Group{}, accountGate: &trueValue, want: true},
		{name: "both on", group: &Group{LongContextPricingEnabled: true}, accountGate: &trueValue, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, effectiveOfficialReferenceLongContextEnabled(tt.group, tt.accountGate))
		})
	}
}

func TestCalculateOfficialReferenceCostUsesCatalogServiceTier(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	base := OfficialReferenceInput{
		BillingMode:  string(BillingModeToken),
		BillingModel: "gpt-6-sol",
		Tokens:       UsageTokens{InputTokens: 1000, OutputTokens: 1000, CacheReadTokens: 1000},
		PricingAt:    time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	}

	standard := CalculateOfficialReferenceCost(billingService, base)
	priorityInput := base
	priorityInput.ServiceTier = "priority"
	priority := CalculateOfficialReferenceCost(billingService, priorityInput)
	flexInput := base
	flexInput.ServiceTier = "flex"
	flex := CalculateOfficialReferenceCost(billingService, flexInput)

	require.NotNil(t, standard)
	require.NotNil(t, priority)
	require.NotNil(t, flex)
	require.Greater(t, priority.Cost, standard.Cost)
	require.Less(t, flex.Cost, standard.Cost)
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
			OfficialReferenceLongContextEnabled: boolPtr(false),
			CreatedAt:                           time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			UserID: 1, Model: "gpt-6-sol", RequestedModel: "gpt-6-sol", UpstreamModel: &ambiguousUpstream,
			BillingMode: &tokenMode, InputTokens: 1000,
			CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
	}
	analysis := &usagestats.BillingAnalysis{
		Total:  usagestats.BillingAnalysisRow{Requests: 3, NonImageRequests: 3},
		Models: []usagestats.BillingAnalysisRow{{Model: "gpt-6-sol", Requests: 3, NonImageRequests: 3}},
	}

	applyBillingAnalysisReferenceCosts(analysis, logs, billingService)

	require.Equal(t, int64(2), analysis.Total.PricedRequests)
	require.InDelta(t, 0.0828188, analysis.Total.OfficialReferenceCost, 1e-12)
	require.Equal(t, analysis.Total.PricedRequests, analysis.Models[0].PricedRequests)
	require.Equal(t, analysis.Total.OfficialReferenceCost, analysis.Models[0].OfficialReferenceCost)
	require.Equal(t, analysis.Total.PricedRequests, analysis.Total.NonImagePricedRequests)
	require.Equal(t, analysis.Total.OfficialReferenceCost, analysis.Total.NonImageOfficialReferenceCost)
	require.Equal(t, analysis.Total.NonImagePricedRequests, analysis.Models[0].NonImagePricedRequests)
	require.Equal(t, analysis.Total.NonImageOfficialReferenceCost, analysis.Models[0].NonImageOfficialReferenceCost)
}

func TestApplyBillingAnalysisReferenceCostsExcludesImagesFromRealRatio(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	tokenMode := string(BillingModeToken)
	imageMode := string(BillingModeImage)
	imageReference := 0.5
	logs := []UsageLog{
		{
			UserID: 1, Model: "gpt-6-sol", BillingMode: &tokenMode,
			InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024,
			OfficialReferenceLongContextEnabled: boolPtr(false),
			CreatedAt:                           time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
		},
		{UserID: 1, Model: "gpt-6-sol", BillingMode: &imageMode, ActualCost: 0.05},
		{UserID: 1, Model: "gpt-6-sol", ImageCount: 1, OfficialReferenceCost: &imageReference},
	}
	row := usagestats.BillingAnalysisRow{
		Requests: 3, UserCost: 1.05, AccountCost: 2,
		NonImageRequests: 1, NonImageUserCost: 1,
	}
	analysis := &usagestats.BillingAnalysis{
		Total: row, Models: []usagestats.BillingAnalysisRow{row},
	}
	analysis.Models[0].Model = "gpt-6-sol"
	users := &usagestats.BillingAnalysisUsers{Users: []usagestats.BillingAnalysisUserRow{{
		UserID: 1, Requests: 3, UserCost: 1.05, AccountCost: 2,
		NonImageRequests: 1, NonImageUserCost: 1,
	}}}

	applyBillingAnalysisReferenceCosts(analysis, logs, billingService)
	applyBillingAnalysisUserReferenceCosts(users, logs, billingService)

	for _, got := range []usagestats.BillingAnalysisRow{analysis.Total, analysis.Models[0]} {
		require.Equal(t, int64(3), got.Requests)
		require.Equal(t, 1.05, got.UserCost)
		require.Equal(t, float64(2), got.AccountCost)
		require.Equal(t, int64(2), got.PricedRequests)
		require.InDelta(t, 0.5428188, got.OfficialReferenceCost, 1e-12)
		require.Equal(t, int64(1), got.NonImageRequests)
		require.Equal(t, int64(1), got.NonImagePricedRequests)
		require.Equal(t, float64(1), got.NonImageUserCost)
		require.InDelta(t, 0.0428188, got.NonImageOfficialReferenceCost, 1e-12)
	}
	require.Equal(t, int64(1), users.Users[0].NonImagePricedRequests)
	require.InDelta(t, 0.0428188, users.Users[0].NonImageOfficialReferenceCost, 1e-12)
	require.Equal(t, 1.05, users.Users[0].UserCost)
	require.Equal(t, float64(2), users.Users[0].AccountCost)
}
