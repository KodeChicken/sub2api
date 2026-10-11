package service

import (
	"testing"
	"time"
)

func TestDetectCarpool7DWindowResetFromUpdates(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	previousReset := now.Add(time.Hour)
	refreshedReset := now.Add(7 * 24 * time.Hour)
	previous := map[string]any{
		"codex_5h_used_percent": 80.0,
		"codex_7d_used_percent": 50.0,
		"codex_7d_reset_at":     previousReset.Format(time.RFC3339),
	}

	key, detected := DetectCarpool7DWindowResetFromUpdates(previous, map[string]any{
		"codex_5h_used_percent": 0.0,
		"codex_7d_used_percent": 1.0,
		"codex_7d_reset_at":     refreshedReset.Format(time.RFC3339),
	}, now)
	if !detected || key != "7d:"+refreshedReset.Format(time.RFC3339) {
		t.Fatalf("1%% after a new 7d reset should be detected, key=%q detected=%v", key, detected)
	}
}

func TestDetectCarpool7DWindowResetIgnores5hOnlyReset(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	resetAt := now.Add(time.Hour).Format(time.RFC3339)
	previous := map[string]any{
		"codex_5h_used_percent": 80.0,
		"codex_7d_used_percent": 50.0,
		"codex_7d_reset_at":     resetAt,
	}
	key, detected := DetectCarpool7DWindowResetFromUpdates(previous, map[string]any{
		"codex_5h_used_percent": 0.0,
		"codex_7d_used_percent": 50.0,
		"codex_7d_reset_at":     resetAt,
	}, now)
	if detected || key != "" {
		t.Fatalf("5h reaching zero must not trigger a carpool reset, key=%q detected=%v", key, detected)
	}
}

func TestDetectCarpool7DWindowResetRequires7dSnapshotChange(t *testing.T) {
	now := time.Date(2026, 9, 12, 4, 0, 0, 0, time.UTC)
	resetAt := now.Add(time.Hour).Format(time.RFC3339)
	previous := map[string]any{"codex_7d_used_percent": 50.0, "codex_7d_reset_at": resetAt}
	key, detected := DetectCarpool7DWindowResetFromUpdates(previous, map[string]any{
		"codex_5h_used_percent": 0.0,
		"codex_7d_reset_at":     resetAt,
	}, now)
	if detected || key != "" {
		t.Fatalf("missing 7d usage must not be interpreted as a reset, key=%q detected=%v", key, detected)
	}
}

func TestAssessCarpool7DWindowResetRequiresUsageDropForAdvancedReset(t *testing.T) {
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	previous := map[string]any{
		"codex_7d_used_percent":  50.0,
		"codex_7d_reset_at":      now.Add(time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at": now.Add(-time.Minute).Format(time.RFC3339),
	}
	decision := assessCarpool7DWindowReset(previous, map[string]any{
		"codex_7d_used_percent": 50.0,
		"codex_7d_reset_at":     now.Add(7 * 24 * time.Hour).Format(time.RFC3339),
	}, now)
	if decision.Detected || decision.Pending != nil {
		t.Fatalf("reset_at advancement without a usage drop must not reset: %+v", decision)
	}
}

func TestAssessCarpool7DWindowResetConfirmsForcedDropTwice(t *testing.T) {
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	baselineAt := now.Add(-time.Minute)
	previous := map[string]any{
		"codex_7d_used_percent":  5.0,
		"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at": baselineAt.Format(time.RFC3339),
	}
	updates := map[string]any{
		"codex_7d_used_percent": 1.0,
		"codex_7d_reset_at":     previous["codex_7d_reset_at"],
	}

	first := assessCarpool7DWindowReset(previous, updates, now)
	if first.Detected || first.Pending == nil {
		t.Fatalf("first forced-drop observation must remain pending: %+v", first)
	}
	previous[carpoolForcedResetCandidateExtraKey] = first.Pending
	tooSoon := assessCarpool7DWindowReset(previous, updates, now.Add(carpoolForcedResetConfirmDelay-time.Second))
	if tooSoon.Detected || tooSoon.Pending == nil {
		t.Fatalf("forced drop must not confirm before the delay: %+v", tooSoon)
	}
	confirmed := assessCarpool7DWindowReset(previous, updates, now.Add(carpoolForcedResetConfirmDelay))
	if !confirmed.Detected || confirmed.EventKey != first.EventKey {
		t.Fatalf("second forced-drop observation should confirm the stable event: first=%+v confirmed=%+v", first, confirmed)
	}
}

func TestAssessCarpool7DWindowResetIgnoresSmallCorrection(t *testing.T) {
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	previous := map[string]any{
		"codex_7d_used_percent":  50.0,
		"codex_7d_reset_at":      now.Add(24 * time.Hour).Format(time.RFC3339),
		"codex_usage_updated_at": now.Add(-time.Minute).Format(time.RFC3339),
	}
	decision := assessCarpool7DWindowReset(previous, map[string]any{
		"codex_7d_used_percent": 49.0,
		"codex_7d_reset_at":     previous["codex_7d_reset_at"],
	}, now)
	if decision.Detected || decision.Pending != nil {
		t.Fatalf("small usage corrections must not reset: %+v", decision)
	}
}

func TestCarpoolQuotaResetStateRetainsBaselineWhilePending(t *testing.T) {
	previous := map[string]any{
		"codex_7d_used_percent":  5.0,
		"codex_usage_updated_at": "2026-10-08T03:59:00Z",
	}
	updates := map[string]any{
		"codex_5h_used_percent":  10.0,
		"codex_7d_used_percent":  1.0,
		"codex_usage_updated_at": "2026-10-08T04:00:00Z",
	}
	candidate := &carpool7DResetCandidate{EventKey: "7d-forced:2026-10-08T03:59:00Z", ObservedAt: "2026-10-08T04:00:00Z"}
	now := time.Date(2026, 10, 8, 4, 0, 0, 0, time.UTC)
	state := nextCarpoolQuotaResetState(previous, updates, carpool7DResetDecision{EventKey: candidate.EventKey, Pending: candidate}, now)
	updates[carpoolQuotaResetStateExtraKey] = state
	persisted := carpoolQuotaResetStateFromExtra(updates)
	if persisted == nil || persisted.Baseline["codex_7d_used_percent"] != 5.0 || persisted.Baseline["codex_usage_updated_at"] != previous["codex_usage_updated_at"] {
		t.Fatal("display updates must not replace the pending detection baseline")
	}
	confirmed := assessCarpool7DWindowReset(persisted.Baseline, updates, now.Add(carpoolForcedResetConfirmDelay))
	if !confirmed.Detected || confirmed.EventKey != candidate.EventKey {
		t.Fatalf("persisted candidate must survive decoding and confirm: %+v", confirmed)
	}
}

func TestCarpoolQuotaSnapshotStaleIgnoresDisplayWrites(t *testing.T) {
	now := time.Date(2026, 10, 11, 4, 0, 0, 0, time.UTC)
	display := map[string]any{"codex_usage_updated_at": now.Format(time.RFC3339)}
	if !carpoolQuotaSnapshotStale(display, now) {
		t.Fatal("a fresh display snapshot must not defer the initial detection")
	}
	state := &carpoolQuotaResetState{
		Baseline:  map[string]any{"codex_7d_used_percent": 80.0},
		CheckedAt: now.Format(time.RFC3339),
	}
	display[carpoolQuotaResetStateExtraKey] = state
	if carpoolQuotaSnapshotStale(display, now.Add(9*time.Minute)) {
		t.Fatal("a recent independent observation should defer the scanner")
	}
	for minute := 10; minute <= 120; minute++ {
		checkedAt := now.Add(time.Duration(minute) * time.Minute)
		display["codex_usage_updated_at"] = checkedAt.Format(time.RFC3339)
		if !carpoolQuotaSnapshotStale(display, checkedAt) {
			t.Fatalf("traffic must not starve the scanner at minute %d", minute)
		}
	}
	state.Baseline[carpoolForcedResetCandidateExtraKey] = &carpool7DResetCandidate{EventKey: "pending", ObservedAt: state.CheckedAt}
	if carpoolQuotaSnapshotStale(display, now.Add(29*time.Second)) || !carpoolQuotaSnapshotStale(display, now.Add(30*time.Second)) {
		t.Fatal("pending observations should be checked again after 30 seconds")
	}
}
