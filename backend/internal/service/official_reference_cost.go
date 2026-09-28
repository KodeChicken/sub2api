package service

import (
	"math"
	"strings"
	"time"
)

// OfficialReferenceInput contains immutable request evidence for catalog-only
// reference pricing. Customer channel/group prices and multipliers are not
// accepted by this API.
type OfficialReferenceInput struct {
	BillingMode               string
	BillingModel              string
	Tokens                    UsageTokens
	ServiceTier               string
	ReasoningEffort           string
	PricingAt                 time.Time
	LongContextPricingEnabled bool
	CustomerComponentCost     float64
	CustomerTotalCost         float64
}

// OfficialReferenceResult is independent from the customer CostBreakdown so
// reference pricing cannot mutate ActualCost or any balance deduction input.
type OfficialReferenceResult struct {
	Cost               float64
	LongContextApplied bool
}

func officialReferenceInputFromCost(
	cost *CostBreakdown,
	tokens UsageTokens,
	serviceTier string,
	reasoningEffort string,
	pricingAt time.Time,
	longContextPricingEnabled bool,
) (OfficialReferenceInput, bool) {
	if cost == nil {
		return OfficialReferenceInput{}, false
	}
	return OfficialReferenceInput{
		BillingMode:               cost.BillingMode,
		BillingModel:              cost.BillingModel,
		Tokens:                    tokens,
		ServiceTier:               serviceTier,
		ReasoningEffort:           reasoningEffort,
		PricingAt:                 pricingAt,
		LongContextPricingEnabled: longContextPricingEnabled,
		CustomerComponentCost: cost.InputCost + cost.ImageInputCost + cost.OutputCost +
			cost.ImageOutputCost + cost.CacheCreationCost + cost.CacheReadCost,
		CustomerTotalCost: cost.TotalCost,
	}, true
}

// CalculateOfficialReferenceCost prices one token usage record with catalog
// pricing only. It never reads or writes the customer CostBreakdown.
func CalculateOfficialReferenceCost(billingService *BillingService, input OfficialReferenceInput) *OfficialReferenceResult {
	if billingService == nil || input.BillingMode != string(BillingModeToken) {
		return nil
	}
	if input.CustomerTotalCost-input.CustomerComponentCost > math.Max(1e-9, math.Abs(input.CustomerTotalCost)*1e-9) {
		// Search and similar additive charges are not persisted with enough detail
		// to reconstruct their catalog price, so exclude them from coverage.
		return nil
	}
	model := strings.TrimSpace(input.BillingModel)
	if model == "" {
		return nil
	}
	breakdown := tryModelFilePricingBreakdown(
		billingService,
		model,
		input.Tokens,
		input.ServiceTier,
		input.PricingAt,
		input.LongContextPricingEnabled,
		input.ReasoningEffort,
	)
	if breakdown == nil || breakdown.TotalCost <= 0 {
		return nil
	}
	return &OfficialReferenceResult{
		Cost:               breakdown.TotalCost,
		LongContextApplied: breakdown.LongContextBillingApplied,
	}
}

func effectiveOfficialReferenceLongContextEnabled(group *Group, accountGate *bool) bool {
	if group != nil && group.LongContextPricingEnabled {
		return true
	}
	return accountGate != nil && *accountGate
}

// OfficialReferenceInputForUsageLog reconstructs catalog-pricing input only
// when the historical billing model is unambiguous. Callers must supply an
// explicitly reviewed historical long-context policy; current settings are
// intentionally not consulted here.
func OfficialReferenceInputForUsageLog(log *UsageLog, longContextPricingEnabled bool, pricingAt time.Time) (OfficialReferenceInput, bool) {
	if log == nil {
		return OfficialReferenceInput{}, false
	}
	billingMode := string(BillingModeToken)
	if log.BillingMode != nil && strings.TrimSpace(*log.BillingMode) != "" {
		billingMode = strings.TrimSpace(*log.BillingMode)
	}
	model, ok := unambiguousHistoricalBillingModel(log)
	if !ok {
		return OfficialReferenceInput{}, false
	}
	return OfficialReferenceInput{
		BillingMode:  billingMode,
		BillingModel: model,
		Tokens: UsageTokens{
			InputTokens:           log.InputTokens,
			ImageInputTokens:      log.ImageInputTokens,
			OutputTokens:          log.OutputTokens,
			CacheCreationTokens:   log.CacheCreationTokens,
			CacheReadTokens:       log.CacheReadTokens,
			CacheCreation5mTokens: log.CacheCreation5mTokens,
			CacheCreation1hTokens: log.CacheCreation1hTokens,
			ImageOutputTokens:     log.ImageOutputTokens,
		},
		ServiceTier:               optionalStringValue(log.ServiceTier),
		ReasoningEffort:           optionalStringValue(log.ReasoningEffort),
		PricingAt:                 pricingAt,
		LongContextPricingEnabled: longContextPricingEnabled,
		CustomerComponentCost: log.InputCost + log.ImageInputCost + log.OutputCost +
			log.ImageOutputCost + log.CacheCreationCost + log.CacheReadCost,
		CustomerTotalCost: log.TotalCost,
	}, true
}

func applyOfficialReferenceResult(log *UsageLog, result *OfficialReferenceResult, enabled bool, pricingAt time.Time) {
	if log == nil || result == nil {
		return
	}
	cost := result.Cost
	applied := result.LongContextApplied
	log.OfficialReferenceCost = &cost
	log.OfficialReferenceLongContextEnabled = boolPtr(enabled)
	log.OfficialReferenceLongContextApplied = &applied
	pricingAtCopy := pricingAt
	log.OfficialReferencePricingAt = &pricingAtCopy
}
