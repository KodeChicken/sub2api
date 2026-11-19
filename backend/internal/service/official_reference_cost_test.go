package service

import (
	"context"
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

func TestApplyOfficialReferenceResultPreservesEvidenceForRetry(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard context", true: "long context enabled"}[enabled], func(t *testing.T) {
			tokenMode := string(BillingModeToken)
			pricingAt := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
			log := UsageLog{
				Model: "gpt-6-sol", BillingMode: &tokenMode,
				InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024,
				ActualCost: 0.01, TotalCost: 0.04, InputCost: 0.04,
			}

			applyOfficialReferenceResult(&log, nil, enabled, pricingAt)

			require.Nil(t, log.OfficialReferenceCost)
			require.Nil(t, log.OfficialReferenceLongContextApplied)
			require.NotNil(t, log.OfficialReferenceLongContextEnabled)
			require.Equal(t, enabled, *log.OfficialReferenceLongContextEnabled)
			require.NotNil(t, log.OfficialReferencePricingAt)
			require.Equal(t, pricingAt, *log.OfficialReferencePricingAt)
			recovered := officialReferenceCostForUsageLog(&log, NewBillingService(nil, nil))
			require.NotNil(t, recovered)
			require.InDelta(t, 0.0428188, *recovered, 1e-12)

			applyOfficialReferenceResult(&log, &OfficialReferenceResult{Cost: *recovered}, enabled, pricingAt)
			require.Equal(t, *recovered, *log.OfficialReferenceCost)
			require.False(t, *log.OfficialReferenceLongContextApplied)
			require.Equal(t, 0.01, log.ActualCost)
			require.Equal(t, 0.04, log.TotalCost)
		})
	}
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

func TestOfficialReferenceCostForUsageLogUsesHistoricalEvidence(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	persisted := 0.04
	require.Equal(t, &persisted, officialReferenceCostForUsageLog(&UsageLog{OfficialReferenceCost: &persisted}, billingService))

	otherModel := "gpt-6-luna"
	log := UsageLog{Model: "gpt-6-sol", InputTokens: 1000, CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}
	require.Nil(t, officialReferenceCostForUsageLog(&log, billingService))
	log.OfficialReferenceLongContextEnabled = boolPtr(false)
	log.UpstreamModel = &otherModel
	require.Nil(t, officialReferenceCostForUsageLog(&log, billingService))
	log.UpstreamModel = nil
	imageMode := string(BillingModeImage)
	log.BillingMode = &imageMode
	require.Nil(t, officialReferenceCostForUsageLog(&log, billingService))
}

type billingAnalysisUsageRepoStub struct {
	usageBatchLogRepoStub
	analysis     *usagestats.BillingAnalysis
	users        *usagestats.BillingAnalysisUsers
	missing      []UsageLog
	missingCalls int
}

func (r *billingAnalysisUsageRepoStub) GetBillingAnalysis(context.Context, usagestats.UsageLogFilters) (*usagestats.BillingAnalysis, error) {
	return r.analysis, nil
}

func (r *billingAnalysisUsageRepoStub) GetBillingAnalysisUsers(context.Context, usagestats.UsageLogFilters, int, int) (*usagestats.BillingAnalysisUsers, error) {
	return r.users, nil
}

func (r *billingAnalysisUsageRepoStub) ListMissingBillingReferenceUsageLogs(context.Context, usagestats.UsageLogFilters) ([]UsageLog, error) {
	r.missingCalls++
	return r.missing, nil
}

func TestBillingAnalysisUsesOnlyPersistedReferenceCosts(t *testing.T) {
	tokenMode := string(BillingModeToken)
	missingReferenceLog := UsageLog{
		UserID: 1, Model: "gpt-6-sol", BillingMode: &tokenMode, ActualCost: 2,
		InputTokens: 467, OutputTokens: 968, CacheReadTokens: 161024,
		OfficialReferenceLongContextEnabled: boolPtr(false),
		CreatedAt:                           time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC),
	}
	total := usagestats.BillingAnalysisRow{
		Requests: 5, PricedRequests: 1, UserCost: 15, AccountCost: 30, OfficialReferenceCost: 0.04, PricedUserCost: 1,
	}
	model := total
	model.Model = missingReferenceLog.Model
	user := usagestats.BillingAnalysisUserRow{
		UserID: 1, Requests: 5, PricedRequests: 1, UserCost: 15, AccountCost: 30, OfficialReferenceCost: 0.04, PricedUserCost: 1,
	}
	repo := &billingAnalysisUsageRepoStub{
		analysis: &usagestats.BillingAnalysis{Total: total, Models: []usagestats.BillingAnalysisRow{model}},
		users:    &usagestats.BillingAnalysisUsers{Users: []usagestats.BillingAnalysisUserRow{user}},
		missing:  []UsageLog{missingReferenceLog},
	}
	billingService := NewBillingService(nil, nil)
	recovered := officialReferenceCostForUsageLog(&missingReferenceLog, billingService)
	require.NotNil(t, recovered)
	require.InDelta(t, 0.0428188, *recovered, 1e-12)
	svc := NewUsageService(repo, nil, nil, nil, billingService)

	analysis, err := svc.GetBillingAnalysis(context.Background(), usagestats.UsageLogFilters{})
	require.NoError(t, err)
	users, err := svc.GetBillingAnalysisUsers(context.Background(), usagestats.UsageLogFilters{Model: model.Model}, 1, 20)
	require.NoError(t, err)
	require.Zero(t, repo.missingCalls, "billing analysis must use persisted aggregates only")

	require.Equal(t, total, analysis.Total)
	require.Equal(t, []usagestats.BillingAnalysisRow{model}, analysis.Models)
	require.Equal(t, []usagestats.BillingAnalysisUserRow{user}, users.Users)
	require.InDelta(t, 25.0, analysis.Total.PricedUserCost/analysis.Total.OfficialReferenceCost, 1e-12, "total ratio")
	require.InDelta(t, 25.0, analysis.Models[0].PricedUserCost/analysis.Models[0].OfficialReferenceCost, 1e-12, "model ratio")
	require.InDelta(t, 25.0, users.Users[0].PricedUserCost/users.Users[0].OfficialReferenceCost, 1e-12, "user ratio")
}
