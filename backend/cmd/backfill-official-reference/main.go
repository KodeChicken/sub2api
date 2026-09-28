package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
)

type options struct {
	start              time.Time
	cutoff             time.Time
	groupID            int64
	model              string
	longContextEnabled bool
	allowCreatedAt     bool
	execute            bool
}

type candidate struct {
	id             int64
	log            service.UsageLog
	actualCostText string
	oldReference   sql.NullFloat64
}

type report struct {
	Mode                   string  `json:"mode"`
	Start                  string  `json:"start"`
	Cutoff                 string  `json:"cutoff"`
	GroupID                int64   `json:"group_id"`
	Model                  string  `json:"model"`
	LongContextEnabled     bool    `json:"long_context_enabled"`
	MatchedRecords         int     `json:"matched_records"`
	PricedRecords          int     `json:"priced_records"`
	SkippedRecords         int     `json:"skipped_records"`
	LongContextApplied     int     `json:"long_context_applied"`
	MatchedUserCost        float64 `json:"matched_user_cost"`
	PricedUserCost         float64 `json:"priced_user_cost"`
	OldReferenceCost       float64 `json:"old_reference_cost"`
	NewReferenceCost       float64 `json:"new_reference_cost"`
	OldRealRatio           float64 `json:"old_real_ratio,omitempty"`
	NewRealRatio           float64 `json:"new_real_ratio,omitempty"`
	MaxSingleRequestDiff   float64 `json:"max_single_request_diff"`
	UpdatedRecords         int64   `json:"updated_records"`
	ActualCostDigestBefore string  `json:"actual_cost_digest_before"`
	ActualCostDigestAfter  string  `json:"actual_cost_digest_after,omitempty"`
	CatalogSHA256          string  `json:"catalog_sha256"`
}

const officialReferenceUpdateSQL = `
	UPDATE usage_logs
	SET official_reference_cost = $1,
		official_reference_long_context_enabled = $2,
		official_reference_long_context_applied = $3,
		official_reference_pricing_at = $4
	WHERE id = $5 AND actual_cost::text = $6`

func main() {
	startRaw := flag.String("start", "", "required RFC3339 inclusive start time")
	cutoffRaw := flag.String("cutoff", "", "required RFC3339 inclusive cutoff time")
	groupID := flag.Int64("group-id", 0, "required usage group id")
	model := flag.String("model", "", "required requested model")
	longContextRaw := flag.String("long-context-enabled", "", "required reviewed historical policy: true or false")
	allowCreatedAt := flag.Bool("allow-created-at", false, "acknowledge that created_at is used when historical pricing_at is absent")
	execute := flag.Bool("execute", false, "persist official_reference_* fields (default is dry-run)")
	flag.Parse()

	opts, err := parseOptions(*startRaw, *cutoffRaw, *groupID, *model, *longContextRaw, *allowCreatedAt, *execute)
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	db, err := sql.Open("postgres", cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	billingService := service.NewBillingService(cfg, service.NewPricingService(cfg, nil))
	result, err := backfill(ctx, db, billingService, opts)
	if err != nil {
		log.Fatalf("backfill failed: %v", err)
	}
	result.CatalogSHA256 = catalogChecksum()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		log.Fatalf("write report: %v", err)
	}
}

func parseOptions(startRaw, cutoffRaw string, groupID int64, model, longContextRaw string, allowCreatedAt, execute bool) (options, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(startRaw))
	if err != nil {
		return options{}, fmt.Errorf("invalid --start: %w", err)
	}
	cutoff, err := time.Parse(time.RFC3339, strings.TrimSpace(cutoffRaw))
	if err != nil {
		return options{}, fmt.Errorf("invalid --cutoff: %w", err)
	}
	if cutoff.Before(start) {
		return options{}, fmt.Errorf("--cutoff must not be before --start")
	}
	if groupID <= 0 {
		return options{}, fmt.Errorf("--group-id must be positive")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return options{}, fmt.Errorf("--model is required")
	}
	longContextEnabled, err := strconv.ParseBool(strings.TrimSpace(longContextRaw))
	if err != nil || strings.TrimSpace(longContextRaw) == "" {
		return options{}, fmt.Errorf("--long-context-enabled must be true or false")
	}
	if !allowCreatedAt {
		return options{}, fmt.Errorf("--allow-created-at is required because historical pricing_at was not persisted")
	}
	return options{
		start: start, cutoff: cutoff, groupID: groupID, model: model,
		longContextEnabled: longContextEnabled, allowCreatedAt: allowCreatedAt, execute: execute,
	}, nil
}

func backfill(ctx context.Context, db *sql.DB, billingService *service.BillingService, opts options) (report, error) {
	result := report{
		Mode: "dry-run", Start: opts.start.Format(time.RFC3339), Cutoff: opts.cutoff.Format(time.RFC3339),
		GroupID: opts.groupID, Model: opts.model, LongContextEnabled: opts.longContextEnabled,
	}
	if opts.execute {
		result.Mode = "execute"
	}

	var tx *sql.Tx
	queryer := queryExecutor(db)
	if opts.execute {
		var err error
		tx, err = db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			return result, err
		}
		defer func() { _ = tx.Rollback() }()
		queryer = tx
	}

	items, err := loadCandidates(ctx, queryer, opts, opts.execute)
	if err != nil {
		return result, err
	}
	result.MatchedRecords = len(items)
	result.ActualCostDigestBefore = actualCostDigest(items)

	type update struct {
		id             int64
		actualCostText string
		cost           float64
		applied        bool
		pricingAt      time.Time
	}
	updates := make([]update, 0, len(items))
	for i := range items {
		item := &items[i]
		actualCost, err := strconv.ParseFloat(item.actualCostText, 64)
		if err != nil {
			return result, fmt.Errorf("parse actual_cost for usage log %d: %w", item.id, err)
		}
		result.MatchedUserCost += actualCost
		if item.oldReference.Valid {
			result.OldReferenceCost += item.oldReference.Float64
		}
		pricingAt := item.log.CreatedAt
		if item.log.OfficialReferencePricingAt != nil {
			pricingAt = *item.log.OfficialReferencePricingAt
		}
		input, ok := service.OfficialReferenceInputForUsageLog(&item.log, opts.longContextEnabled, pricingAt)
		if !ok {
			result.SkippedRecords++
			continue
		}
		reference := service.CalculateOfficialReferenceCost(billingService, input)
		if reference == nil {
			result.SkippedRecords++
			continue
		}
		result.PricedRecords++
		result.PricedUserCost += actualCost
		result.NewReferenceCost += reference.Cost
		if reference.LongContextApplied {
			result.LongContextApplied++
		}
		oldCost := 0.0
		if item.oldReference.Valid {
			oldCost = item.oldReference.Float64
		}
		result.MaxSingleRequestDiff = math.Max(result.MaxSingleRequestDiff, math.Abs(reference.Cost-oldCost))
		updates = append(updates, update{item.id, item.actualCostText, reference.Cost, reference.LongContextApplied, pricingAt})
	}
	if result.OldReferenceCost > 0 {
		result.OldRealRatio = result.MatchedUserCost / result.OldReferenceCost
	}
	if result.NewReferenceCost > 0 {
		result.NewRealRatio = result.PricedUserCost / result.NewReferenceCost
	}
	if !opts.execute {
		return result, nil
	}

	for _, item := range updates {
		execResult, err := tx.ExecContext(ctx, officialReferenceUpdateSQL,
			item.cost, opts.longContextEnabled, item.applied, item.pricingAt, item.id, item.actualCostText)
		if err != nil {
			return result, err
		}
		rows, err := execResult.RowsAffected()
		if err != nil {
			return result, err
		}
		if rows != 1 {
			return result, fmt.Errorf("usage log %d actual_cost changed during backfill", item.id)
		}
		result.UpdatedRecords += rows
	}
	after, err := loadActualCosts(ctx, tx, opts)
	if err != nil {
		return result, err
	}
	result.ActualCostDigestAfter = actualCostDigest(after)
	if result.ActualCostDigestAfter != result.ActualCostDigestBefore {
		return result, fmt.Errorf("actual_cost digest changed: before=%s after=%s", result.ActualCostDigestBefore, result.ActualCostDigestAfter)
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

type queryExecutor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadCandidates(ctx context.Context, q queryExecutor, opts options, lock bool) ([]candidate, error) {
	query := `
		SELECT id, model, requested_model, upstream_model, upstream_response_model,
			input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens,
			cache_creation_5m_tokens, cache_creation_1h_tokens,
			image_input_tokens, image_output_tokens,
			input_cost, image_input_cost, output_cost, image_output_cost,
			cache_creation_cost, cache_read_cost, total_cost, actual_cost::text,
			billing_mode, service_tier, reasoning_effort, created_at,
			official_reference_cost, official_reference_pricing_at
		FROM usage_logs
		WHERE group_id = $1
		  AND created_at >= $2
		  AND created_at <= $3
		  AND COALESCE(NULLIF(requested_model, ''), model) = $4
		ORDER BY id`
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := q.QueryContext(ctx, query, opts.groupID, opts.start, opts.cutoff, opts.model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		var requestedModel, upstreamModel, upstreamResponseModel sql.NullString
		var billingMode, serviceTier, reasoningEffort sql.NullString
		var pricingAt sql.NullTime
		if err := rows.Scan(
			&item.id, &item.log.Model, &requestedModel, &upstreamModel, &upstreamResponseModel,
			&item.log.InputTokens, &item.log.OutputTokens, &item.log.CacheCreationTokens, &item.log.CacheReadTokens,
			&item.log.CacheCreation5mTokens, &item.log.CacheCreation1hTokens,
			&item.log.ImageInputTokens, &item.log.ImageOutputTokens,
			&item.log.InputCost, &item.log.ImageInputCost, &item.log.OutputCost, &item.log.ImageOutputCost,
			&item.log.CacheCreationCost, &item.log.CacheReadCost, &item.log.TotalCost, &item.actualCostText,
			&billingMode, &serviceTier, &reasoningEffort, &item.log.CreatedAt,
			&item.oldReference, &pricingAt,
		); err != nil {
			return nil, err
		}
		item.log.ID = item.id
		item.log.RequestedModel = nullStringValue(requestedModel)
		item.log.UpstreamModel = nullStringPtr(upstreamModel)
		item.log.UpstreamResponseModel = nullStringPtr(upstreamResponseModel)
		item.log.BillingMode = nullStringPtr(billingMode)
		item.log.ServiceTier = nullStringPtr(serviceTier)
		item.log.ReasoningEffort = nullStringPtr(reasoningEffort)
		if pricingAt.Valid {
			value := pricingAt.Time
			item.log.OfficialReferencePricingAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadActualCosts(ctx context.Context, q queryExecutor, opts options) ([]candidate, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, actual_cost::text
		FROM usage_logs
		WHERE group_id = $1
		  AND created_at >= $2
		  AND created_at <= $3
		  AND COALESCE(NULLIF(requested_model, ''), model) = $4
		ORDER BY id`, opts.groupID, opts.start, opts.cutoff, opts.model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.actualCostText); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func actualCostDigest(items []candidate) string {
	hash := sha256.New()
	for _, item := range items {
		_, _ = fmt.Fprintf(hash, "%d=%s\n", item.id, item.actualCostText)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	result := strings.TrimSpace(value.String)
	return &result
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}

func catalogChecksum() string {
	candidates := []string{
		filepath.Join("resources", "model-pricing", "model_prices_and_context_window.json"),
		filepath.Join("backend", "resources", "model-pricing", "model_prices_and_context_window.json"),
	}
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(content)
		return hex.EncodeToString(digest[:])
	}
	return "unavailable"
}
