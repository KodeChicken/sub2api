package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration246AddsNullableOfficialReferenceAuditFields(t *testing.T) {
	content, err := FS.ReadFile("246_usage_log_official_reference_audit.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "official_reference_long_context_enabled BOOLEAN")
	require.Contains(t, sql, "official_reference_long_context_applied BOOLEAN")
	require.Contains(t, sql, "official_reference_pricing_at TIMESTAMPTZ")
	require.NotContains(t, sql, "DEFAULT false")
	require.NotContains(t, sql, "actual_cost")
}
