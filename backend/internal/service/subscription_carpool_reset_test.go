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
