package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

// Keep this separate from Antigravity's switch, which also admits Gemini.
func (a *Account) IsKiroMixedSchedulingEnabled() bool {
	return a != nil && a.Platform == PlatformKiro &&
		(a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey) &&
		a.Extra["mixed_scheduling"] == true
}

func accountHasGroupID(a *Account, groupID int64) bool {
	if a == nil || groupID <= 0 {
		return false
	}
	for _, id := range a.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, binding := range a.AccountGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	return false
}

// A finite configured alias is evidence of administrator intent, not upstream entitlement.
func kiroModelKey(model string) string {
	lower := strings.ToLower(model)
	if strings.HasSuffix(lower, "-latest") || strings.HasSuffix(lower, "-thinking") {
		return lower
	}
	normalized, err := kiro.ModelID(model)
	if err != nil {
		return model
	}
	return normalized
}

func concreteKiroModel(model string) bool {
	if model == "" || strings.TrimSpace(model) != model || strings.Contains(model, "*") {
		return false
	}
	_, err := kiro.ModelID(model)
	return err == nil
}

func (a *Account) resolveFiniteKiroModel(requested string) (string, bool, error) {
	mapping := a.GetModelMapping()
	if target, ok := mapping[requested]; ok && concreteKiroModel(requested) && concreteKiroModel(target) {
		canonical, _ := kiro.ModelID(target)
		return canonical, true, nil
	}
	key := kiroModelKey(requested)
	var target string
	for public, upstream := range mapping {
		if !concreteKiroModel(public) || !concreteKiroModel(upstream) || kiroModelKey(public) != key {
			continue
		}
		canonical, _ := kiro.ModelID(upstream)
		if target != "" && target != canonical {
			return "", false, errors.New("ambiguous Kiro model mapping; configure an exact public alias")
		}
		target = canonical
	}
	if target == "" {
		return "", false, nil
	}
	return target, true, nil
}

func (a *Account) kiroFiniteModels() map[string]string {
	result := make(map[string]string)
	for public := range a.GetModelMapping() {
		if target, ok, err := a.resolveFiniteKiroModel(public); err == nil && ok {
			result[public] = target
		}
	}
	return result
}

func kiroMixedAccountInGroup(a *Account, group *Group) bool {
	return group != nil && group.Platform == PlatformAnthropic && group.Status == StatusActive &&
		a.IsKiroMixedSchedulingEnabled() && accountHasGroupID(a, group.ID) && len(a.kiroFiniteModels()) > 0
}

func (s *GatewayService) kiroMixedRequestGroup(ctx context.Context, groupID *int64, platform string) *Group {
	if groupID == nil || platform != PlatformAnthropic {
		return nil
	}
	if forced, _ := ctx.Value(ctxkey.ForcePlatform).(string); forced != "" {
		return nil
	}
	if resolved, ok := ResolvedTargetPlatformFromContext(ctx); ok && resolved != PlatformAnthropic {
		return nil
	}
	group := s.groupFromContext(ctx, *groupID)
	if group == nil && s.groupRepo != nil {
		group, _ = s.groupRepo.GetByIDLite(ctx, *groupID)
	}
	if group == nil || group.Platform != PlatformAnthropic || group.Status != StatusActive {
		return nil
	}
	return group
}

func kiroMixedRequestAllowed(ctx context.Context, a *Account, group *Group, model string) bool {
	if forced, _ := ctx.Value(ctxkey.ForcePlatform).(string); forced != "" {
		return false
	}
	if resolved, ok := ResolvedTargetPlatformFromContext(ctx); ok && resolved != PlatformAnthropic {
		return false
	}
	if !kiroMixedAccountInGroup(a, group) {
		return false
	}
	if model == "" {
		return true
	}
	_, matched, err := a.resolveFiniteKiroModel(model)
	return matched && err == nil
}

func (s *GatewayService) filterKiroMixedAccounts(ctx context.Context, accounts []Account, groupID *int64, platform string) []Account {
	group := s.kiroMixedRequestGroup(ctx, groupID, platform)
	filtered := make([]Account, 0, len(accounts))
	for _, a := range accounts {
		if a.Platform == PlatformKiro && platform != PlatformKiro && !kiroMixedRequestAllowed(ctx, &a, group, "") {
			continue
		}
		filtered = append(filtered, a)
	}
	return filtered
}
