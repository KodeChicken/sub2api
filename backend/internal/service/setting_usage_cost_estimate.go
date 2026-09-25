package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UsageCostEstimate configures a global estimate per weekly book-value capacity.
type UsageCostEstimate struct {
	WeeklyCostUSD  float64 `json:"weekly_cost_usd"`
	WeeklyQuotaUSD float64 `json:"weekly_quota_usd"`
}

func validUsageCostEstimate(value UsageCostEstimate) bool {
	if math.IsNaN(value.WeeklyCostUSD) || math.IsInf(value.WeeklyCostUSD, 0) ||
		math.IsNaN(value.WeeklyQuotaUSD) || math.IsInf(value.WeeklyQuotaUSD, 0) {
		return false
	}
	if value.WeeklyCostUSD == 0 && value.WeeklyQuotaUSD == 0 {
		return true
	}
	ratio := value.WeeklyCostUSD / value.WeeklyQuotaUSD
	return value.WeeklyCostUSD > 0 && value.WeeklyQuotaUSD > 0 &&
		ratio > 0 && !math.IsInf(ratio, 0)
}

func (s *SettingService) GetUsageCostEstimate(ctx context.Context) (*UsageCostEstimate, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyUsageCostEstimate)
	if errors.Is(err, ErrSettingNotFound) {
		return &UsageCostEstimate{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value UsageCostEstimate
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("decode usage cost estimate: %w", err)
	}
	if !validUsageCostEstimate(value) {
		return nil, fmt.Errorf("invalid stored usage cost estimate")
	}
	return &value, nil
}

func (s *SettingService) SetUsageCostEstimate(ctx context.Context, value UsageCostEstimate) error {
	if !validUsageCostEstimate(value) {
		return infraerrors.BadRequest("INVALID_USAGE_COST_ESTIMATE", "weekly_cost_usd and weekly_quota_usd must both be positive, or both zero to clear")
	}
	if value.WeeklyCostUSD == 0 {
		return s.settingRepo.Delete(ctx, SettingKeyUsageCostEstimate)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyUsageCostEstimate, string(data))
}
