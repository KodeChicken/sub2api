package service

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

type kiroCredits struct {
	Used         *float64 `json:"currentUsage"`
	Limit        *float64 `json:"usageLimit"`
	PreciseUsed  *float64 `json:"currentUsageWithPrecision"`
	PreciseLimit *float64 `json:"usageLimitWithPrecision"`
}

func (c kiroCredits) totals() (float64, float64, bool) {
	used, limit := c.Used, c.Limit
	if c.PreciseUsed != nil {
		used = c.PreciseUsed
	}
	if c.PreciseLimit != nil {
		limit = c.PreciseLimit
	}
	if used == nil || limit == nil || *used < 0 || *limit < 0 || math.IsNaN(*used) || math.IsNaN(*limit) ||
		math.IsInf(*used, 0) || math.IsInf(*limit, 0) {
		return 0, 0, false
	}
	return *used, *limit, true
}

func parseKiroUsage(data []byte, now time.Time) (*UsageInfo, error) {
	var payload struct {
		NextReset    *float64 `json:"nextDateReset"`
		Subscription struct {
			Title string `json:"subscriptionTitle"`
		} `json:"subscriptionInfo"`
		Breakdowns []struct {
			kiroCredits
			Resource  string   `json:"resourceType"`
			NextReset *float64 `json:"nextDateReset"`
			Trial     *struct {
				kiroCredits
				Status string   `json:"freeTrialStatus"`
				Expiry *float64 `json:"freeTrialExpiry"`
			} `json:"freeTrialInfo"`
			Bonuses []struct {
				kiroCredits
				Status string   `json:"status"`
				Expiry *float64 `json:"expiresOn"`
			} `json:"bonuses"`
		} `json:"usageBreakdownList"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, errors.New("invalid Kiro quota response")
	}
	info := &UsageInfo{Source: "active", UpdatedAt: &now, SubscriptionTierRaw: payload.Subscription.Title}
	validExpiry := func(expiry *float64) bool { return expiry == nil || *expiry > float64(now.Unix()) }
	for _, breakdown := range payload.Breakdowns {
		if !strings.EqualFold(breakdown.Resource, "CREDIT") && !(len(payload.Breakdowns) == 1 && breakdown.Resource == "") {
			continue
		}
		used, limit, ok := breakdown.totals()
		if !ok {
			return info, nil
		}
		if trial := breakdown.Trial; trial != nil && trial.Status == "ACTIVE" && validExpiry(trial.Expiry) {
			trialUsed, trialLimit, valid := trial.totals()
			if !valid {
				return info, nil
			}
			used, limit = used+trialUsed, limit+trialLimit
		}
		for _, bonus := range breakdown.Bonuses {
			if bonus.Status != "ACTIVE" || !validExpiry(bonus.Expiry) {
				continue
			}
			bonusUsed, bonusLimit, valid := bonus.totals()
			if !valid {
				return info, nil
			}
			used, limit = used+bonusUsed, limit+bonusLimit
		}
		if limit <= 0 {
			return info, nil
		}
		progress := &UsageProgress{Utilization: used / limit * 100}
		reset := payload.NextReset
		if reset == nil {
			reset = breakdown.NextReset
		}
		if reset != nil && *reset > 0 && *reset < 253402300800 {
			at := time.Unix(int64(*reset), 0).UTC()
			progress.ResetsAt = &at
			progress.RemainingSeconds = max(0, int(at.Sub(now).Seconds()))
		}
		info.ThirtyDay = progress
		info.AICredits = []AICredit{{CreditType: "Kiro Credits", Amount: math.Max(0, limit-used)}}
		return info, nil
	}
	return info, nil
}
