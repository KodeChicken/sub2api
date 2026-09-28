package service

import (
	"math"
	"strings"
	"time"
)

// calculateOfficialReferenceCost prices one token usage record with catalog
// pricing only. Customer channel/group prices are already reflected in
// ActualCost and must not leak into the reference denominator.
func calculateOfficialReferenceCost(
	billingService *BillingService,
	cost *CostBreakdown,
	tokens UsageTokens,
	serviceTier string,
	reasoningEffort string,
	pricingAt time.Time,
) *float64 {
	if billingService == nil || cost == nil || cost.BillingMode != string(BillingModeToken) {
		return nil
	}
	componentTotal := cost.InputCost + cost.ImageInputCost + cost.OutputCost + cost.ImageOutputCost + cost.CacheCreationCost + cost.CacheReadCost
	if cost.TotalCost-componentTotal > math.Max(1e-9, math.Abs(cost.TotalCost)*1e-9) {
		// Search and similar additive charges are not persisted with enough detail
		// to reconstruct their catalog price, so exclude them from coverage.
		return nil
	}
	model := strings.TrimSpace(cost.BillingModel)
	if model == "" {
		return nil
	}
	return tryModelFilePricing(
		billingService,
		model,
		tokens,
		serviceTier,
		pricingAt,
		cost.LongContextBillingApplied,
		reasoningEffort,
	)
}
