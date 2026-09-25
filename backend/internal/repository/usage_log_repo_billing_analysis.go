package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
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
				actual_cost AS user_cost,
				COALESCE(account_stats_cost, total_cost) * COALESCE(account_rate_multiplier, 1) AS account_cost
			FROM usage_logs
			%s
		)
		SELECT GROUPING(model), model,
			COUNT(*), COALESCE(SUM(user_cost), 0), COALESCE(SUM(account_cost), 0)
		FROM scoped
		GROUP BY GROUPING SETS ((), (model))
	`, resolveModelDimensionExpression(usagestats.ModelSourceRequested), buildWhere(conditions))
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := &usagestats.BillingAnalysis{
		Models: make([]usagestats.BillingAnalysisRow, 0),
	}
	for rows.Next() {
		var modelGrouped int
		var model *string
		var row usagestats.BillingAnalysisRow
		if err := rows.Scan(&modelGrouped, &model, &row.Requests, &row.UserCost, &row.AccountCost); err != nil {
			return nil, err
		}
		if modelGrouped == 1 {
			result.Total = row
		} else {
			if model != nil {
				row.Model = *model
			}
			result.Models = append(result.Models, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
