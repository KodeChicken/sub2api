package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

var candidateColumns = []string{
	"id", "model", "requested_model", "upstream_model", "upstream_response_model",
	"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
	"cache_creation_5m_tokens", "cache_creation_1h_tokens",
	"image_input_tokens", "image_output_tokens",
	"input_cost", "image_input_cost", "output_cost", "image_output_cost",
	"cache_creation_cost", "cache_read_cost", "total_cost", "actual_cost",
	"billing_mode", "service_tier", "reasoning_effort", "created_at",
	"official_reference_cost", "official_reference_pricing_at",
}

func TestParseOptionsRequiresHistoricalSafetyAcknowledgements(t *testing.T) {
	start := "2026-09-28T00:00:00+08:00"
	cutoff := "2026-09-28T21:22:09+08:00"

	_, err := parseOptions(start, cutoff, 7, "gpt-6-sol", "", true, false)
	require.ErrorContains(t, err, "--long-context-enabled")

	_, err = parseOptions(start, cutoff, 7, "gpt-6-sol", "true", false, false)
	require.ErrorContains(t, err, "--allow-created-at")

	opts, err := parseOptions(start, cutoff, 7, "gpt-6-sol", "true", true, false)
	require.NoError(t, err)
	require.False(t, opts.execute)
}

func TestOfficialReferenceUpdateSQLDoesNotAssignActualCost(t *testing.T) {
	setClause := strings.SplitN(strings.ToLower(officialReferenceUpdateSQL), "where", 2)[0]
	require.NotContains(t, setClause, "actual_cost")
	require.Contains(t, strings.ToLower(officialReferenceUpdateSQL), "where id = $5 and actual_cost::text = $6")
}

func TestBackfillDryRunDoesNotWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	opts := testOptions(false)
	mock.ExpectQuery("SELECT id, model, requested_model").
		WithArgs(opts.groupID, opts.start, opts.cutoff, opts.model).
		WillReturnRows(testCandidateRows("0.010000", "0.020000"))

	result, err := backfill(context.Background(), db, service.NewBillingService(nil, nil), opts)
	require.NoError(t, err)
	require.Equal(t, "dry-run", result.Mode)
	require.Equal(t, 1, result.MatchedRecords)
	require.Equal(t, 1, result.PricedRecords)
	require.Zero(t, result.UpdatedRecords)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBackfillDigestMismatchRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	opts := testOptions(true)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, model, requested_model").
		WithArgs(opts.groupID, opts.start, opts.cutoff, opts.model).
		WillReturnRows(testCandidateRows("0.010000", "0.020000"))
	mock.ExpectExec(regexp.QuoteMeta(strings.TrimSpace(officialReferenceUpdateSQL))).
		WithArgs(sqlmock.AnyArg(), true, sqlmock.AnyArg(), opts.start, int64(101), "0.010000").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id, actual_cost::text").
		WithArgs(opts.groupID, opts.start, opts.cutoff, opts.model).
		WillReturnRows(sqlmock.NewRows([]string{"id", "actual_cost"}).AddRow(int64(101), "0.010001"))
	mock.ExpectRollback()

	_, err = backfill(context.Background(), db, service.NewBillingService(nil, nil), opts)
	require.ErrorContains(t, err, "actual_cost digest changed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func testOptions(execute bool) options {
	start := time.Date(2026, 9, 28, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	return options{
		start:              start,
		cutoff:             time.Date(2026, 9, 28, 21, 22, 9, 0, start.Location()),
		groupID:            7,
		model:              "gpt-6-sol",
		longContextEnabled: true,
		allowCreatedAt:     true,
		execute:            execute,
	}
}

func testCandidateRows(actualCost, oldReference string) *sqlmock.Rows {
	createdAt := testOptions(false).start
	return sqlmock.NewRows(candidateColumns).AddRow(
		int64(101), "gpt-6-sol", "gpt-6-sol", nil, nil,
		1000, 500, 0, 2000, 0, 0, 0, 0,
		0.001, 0.0, 0.002, 0.0, 0.0, 0.001, 0.004, actualCost,
		"token", "", "", createdAt, oldReference, nil,
	)
}
