package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLockAndMergeAccountExtraPreservesLatestAutoResetAttempt(t *testing.T) {
	for _, tc := range []struct {
		name           string
		currentEnabled bool
		explicit       *bool
	}{
		{name: "reauthorization cannot overwrite concurrently disabled switch"},
		{name: "edit preserves enabled switch", currentEnabled: true},
		{name: "explicit disable", currentEnabled: true, explicit: boolPtrForAutoReset(false)},
		{name: "explicit enable initializes again", explicit: boolPtrForAutoReset(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			state := map[string]any{
				"status": "failed", "expiry_initialized": true,
				"attempt_credit_hash": "original", "attempt_cycle_hash": "old-cycle",
				"attempt_expires_at": "2026-11-06T05:00:00Z", "attempt_consumed": true,
				"unresolved_earliest_until": "unknown",
			}
			current := map[string]any{
				service.OpenAIAutoResetCreditExpiryEnabledExtraKey: tc.currentEnabled,
				service.OpenAIAutoResetCreditStateExtraKey:         state,
				"codex_reset_credit_snapshot":                      map[string]any{"available_count": float64(0)},
			}
			currentJSON, err := json.Marshal(current)
			require.NoError(t, err)
			mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT")+`.*`+regexp.QuoteMeta("FOR NO KEY UPDATE")).
				WithArgs(int64(700), service.PlatformOpenAI, service.AccountTypeOAuth, `{}`, nil).
				WillReturnRows(sqlmock.NewRows(openCodeGoMergeMockColumns()).
					AddRow(true, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, nil, nil, currentJSON))
			ctx := context.Background()
			if tc.explicit != nil {
				ctx = service.WithOpenAIAutoResetCreditExpiryUpdate(ctx, *tc.explicit)
			}
			account := &service.Account{ID: 700, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Extra: map[string]any{
					service.OpenAIAutoResetCreditExpiryEnabledExtraKey: !tc.currentEnabled,
					service.OpenAIAutoResetCreditStateExtraKey:         map[string]any{"status": "success", "attempt_credit_hash": "forged"},
					"codex_reset_credit_snapshot":                      map[string]any{"available_count": 100},
				},
			}
			got, err := lockAndMergeAccountProbeExtra(ctx, client, account, nil, nil)
			require.NoError(t, err)
			wantEnabled := tc.currentEnabled
			if tc.explicit != nil {
				wantEnabled = *tc.explicit
			}
			if wantEnabled && !tc.currentEnabled {
				state["expiry_initialized"] = false
			}
			require.Equal(t, wantEnabled, got[service.OpenAIAutoResetCreditExpiryEnabledExtraKey])
			require.Equal(t, state, got[service.OpenAIAutoResetCreditStateExtraKey])
			require.Equal(t, current["codex_reset_credit_snapshot"], got["codex_reset_credit_snapshot"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func boolPtrForAutoReset(value bool) *bool {
	return &value
}
