package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/lib/pq"
)

// GetBillingAnalysis aggregates persisted charges and account estimates with
// exactly the same request filters, without the model chart's display override.
func (r *usageLogRepository) GetBillingAnalysis(ctx context.Context, filters usagestats.UsageLogFilters) (*usagestats.BillingAnalysis, error) {
	conditions := make([]string, 0, 12)
	args := make([]any, 0, 12)
	addID := func(column string, id int64) {
		if id > 0 {
			conditions = append(conditions, fmt.Sprintf("%s = $%d", column, len(args)+1))
			args = append(args, id)
		}
	}
	addID("user_id", filters.UserID)
	addID("api_key_id", filters.APIKeyID)
	addID("account_id", filters.AccountID)
	addID("group_id", filters.GroupID)
	if filters.RequestID != "" {
		conditions = append(conditions, fmt.Sprintf("request_id = $%d", len(args)+1))
		args = append(args, filters.RequestID)
	}
	conditions, args = appendUsageLogModelWhereCondition(conditions, args, filters.Model, filters.ModelFilterSource)
	conditions, args = appendRequestTypeOrStreamWhereCondition(conditions, args, filters.RequestType, filters.Stream)
	conditions, args = appendNativeCompactionV2WhereCondition(conditions, args, filters.NativeCompactionV2, "")
	if filters.BillingType != nil {
		conditions = append(conditions, fmt.Sprintf("billing_type = $%d", len(args)+1))
		args = append(args, int16(*filters.BillingType))
	}
	conditions, args = appendUsageLogBillingModeWhereCondition(conditions, args, filters.BillingMode)
	if filters.UpstreamModelMismatch != nil {
		conditions = append(conditions, upstreamModelMismatchCondition("upstream_model_mismatch", *filters.UpstreamModelMismatch))
	}
	if filters.StartTime != nil {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", len(args)+1))
		args = append(args, *filters.StartTime)
	}
	if filters.EndTime != nil {
		conditions = append(conditions, fmt.Sprintf("created_at < $%d", len(args)+1))
		args = append(args, *filters.EndTime)
	}

	query := fmt.Sprintf(`
		WITH scoped AS (
			SELECT
				%s AS model,
				account_id,
				actual_cost AS user_cost,
				COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1) AS account_cost
			FROM usage_logs
			%s
		)
		SELECT GROUPING(model), GROUPING(account_id), model, account_id,
			COUNT(*), COALESCE(SUM(user_cost), 0), COALESCE(SUM(account_cost), 0)
		FROM scoped
		GROUP BY GROUPING SETS ((), (model), (account_id))
	`, resolveModelDimensionExpression(usagestats.ModelSourceRequested), buildWhere(conditions))
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := &usagestats.BillingAnalysis{
		Models:   make([]usagestats.BillingAnalysisRow, 0),
		Accounts: make([]usagestats.BillingAnalysisRow, 0),
	}
	for rows.Next() {
		var modelGrouped, accountGrouped int
		var model *string
		var accountID *int64
		var row usagestats.BillingAnalysisRow
		if err := rows.Scan(&modelGrouped, &accountGrouped, &model, &accountID, &row.Requests, &row.UserCost, &row.AccountCost); err != nil {
			return nil, err
		}
		switch {
		case modelGrouped == 1 && accountGrouped == 1:
			result.Total = row
		case modelGrouped == 0:
			if model != nil {
				row.Model = *model
			}
			result.Models = append(result.Models, row)
		case accountGrouped == 0 && accountID != nil:
			row.AccountID = *accountID
			result.Accounts = append(result.Accounts, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := r.fillBillingAccountEstimates(ctx, result.Accounts); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *usageLogRepository) fillBillingAccountEstimates(ctx context.Context, accounts []usagestats.BillingAnalysisRow) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(accounts))
	positions := make(map[int64]int, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].AccountID)
		positions[accounts[i].AccountID] = i
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id, extra FROM accounts WHERE id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return err
	}
	type window struct {
		id    int64
		start time.Time
		used  float64
	}
	windows := make([]window, 0, len(accounts))
	now := time.Now()
	for rows.Next() {
		var id int64
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			_ = rows.Close()
			return err
		}
		var extra map[string]any
		if err := json.Unmarshal(raw, &extra); err != nil {
			_ = rows.Close()
			return err
		}
		row := &accounts[positions[id]]
		if monthly := billingExtraNumber(extra["monthly_cost_usd"]); monthly > 0 {
			row.MonthlyCost = &monthly
		}
		used := billingExtraNumber(extra["codex_7d_used_percent"])
		if used <= 0 || used > 100 {
			continue
		}
		updated, err := time.Parse(time.RFC3339, fmt.Sprint(extra["codex_usage_updated_at"]))
		if err != nil || now.Sub(updated) > 24*time.Hour || updated.After(now.Add(time.Minute)) {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339, fmt.Sprint(extra["codex_7d_reset_at"]))
		if err != nil || !now.Before(resetAt) || resetAt.After(now.Add(8*24*time.Hour)) {
			continue
		}
		windows = append(windows, window{id: id, start: resetAt.Add(-7 * 24 * time.Hour), used: used})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(windows) == 0 {
		return nil
	}
	args := make([]any, 0, len(windows)*2)
	values := make([]string, 0, len(windows))
	for _, item := range windows {
		values = append(values, fmt.Sprintf("($%d::bigint, $%d::timestamptz)", len(args)+1, len(args)+2))
		args = append(args, item.id, item.start)
	}
	query := fmt.Sprintf(`
		SELECT w.id, COALESCE(SUM(COALESCE(u.account_stats_cost, u.total_cost) * COALESCE(u.account_rate_multiplier, 1)), 0)
		FROM (VALUES %s) AS w(id, start_at)
		LEFT JOIN usage_logs u ON u.account_id = w.id AND u.created_at >= w.start_at
		GROUP BY w.id
	`, strings.Join(values, ","))
	statsRows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = statsRows.Close() }()
	usedByID := make(map[int64]float64, len(windows))
	for _, item := range windows {
		usedByID[item.id] = item.used
	}
	for statsRows.Next() {
		var id int64
		var cost float64
		if err := statsRows.Scan(&id, &cost); err != nil {
			return err
		}
		estimate := cost * 100 / usedByID[id]
		if estimate > 0 && !math.IsInf(estimate, 0) && !math.IsNaN(estimate) {
			accounts[positions[id]].SevenDayEstimate = &estimate
		}
	}
	return statsRows.Err()
}

func billingExtraNumber(raw any) float64 {
	switch value := raw.(type) {
	case float64:
		if math.IsInf(value, 0) || math.IsNaN(value) || value < 0 {
			return 0
		}
		return value
	case string:
		number, err := strconv.ParseFloat(value, 64)
		if err == nil && !math.IsInf(number, 0) && !math.IsNaN(number) && number >= 0 {
			return number
		}
	}
	return 0
}
