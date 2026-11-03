package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.KiroOAuthRefreshPersistence = (*accountRepository)(nil)
var _ service.KiroOAuthReauthPersistence = (*accountRepository)(nil)

func (r *accountRepository) ApplyKiroOAuthReauthorization(ctx context.Context, id int64, expected map[string]any, proxyID *int64, credentials map[string]any) (bool, error) {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return false, errors.New("Kiro reauthorization requires a transaction")
	}
	before, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return false, err
	}
	result, err := tx.Client().ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts AS a SET credentials=$1::jsonb,
				status=CASE WHEN a.status='error' THEN 'active' ELSE a.status END,
				error_message=CASE WHEN a.status='error' THEN '' ELSE a.error_message END,
				extra=COALESCE(a.extra,'{}'::jsonb)-'kiro_profile_checked_at'-'kiro_profile_credentials_hash'-'kiro_profile_arn',
				updated_at=NOW()
			WHERE a.id=$2 AND a.deleted_at IS NULL AND a.platform=$3 AND a.type=$4
				AND a.credentials=$5::jsonb AND a.proxy_id IS NOT DISTINCT FROM $6
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload)
		SELECT $7,updated.id,NULL,NULL FROM updated`, string(after), id, service.PlatformKiro,
		service.AccountTypeOAuth, string(before), proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}

func (r *accountRepository) UpdateKiroOAuthCredentialsIfUnchanged(ctx context.Context, id int64, expected map[string]any, proxyID *int64, credentials map[string]any) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("Kiro account SQL executor is not configured")
	}
	before, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts AS a SET credentials = $1::jsonb, updated_at = NOW()
			WHERE a.id = $2 AND a.deleted_at IS NULL
				AND a.platform = $3 AND a.type = $4 AND a.status = $5
				AND a.credentials = $6::jsonb AND a.proxy_id IS NOT DISTINCT FROM $7
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $8, updated.id, NULL, NULL FROM updated`,
		string(after), id, service.PlatformKiro, service.AccountTypeOAuth, service.StatusActive,
		string(before), proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 {
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
	}
	return count > 0, nil
}

// A nil cooldown marks a permanent credential error; otherwise preserve account status and scheduling switches.
func (r *accountRepository) SetKiroOAuthRefreshFailureIfUnchanged(ctx context.Context, id int64, expected map[string]any, proxyID *int64, cooldown *time.Time, reason string) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("Kiro account SQL executor is not configured")
	}
	before, err := json.Marshal(normalizeJSONMap(expected))
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts AS a SET
				status = CASE WHEN $1::timestamptz IS NULL THEN 'error' ELSE a.status END,
				error_message = CASE WHEN $1::timestamptz IS NULL THEN $2 ELSE a.error_message END,
				schedulable = CASE WHEN $1::timestamptz IS NULL THEN FALSE ELSE a.schedulable END,
				temp_unschedulable_until = CASE WHEN $1::timestamptz IS NULL
					THEN a.temp_unschedulable_until ELSE GREATEST(a.temp_unschedulable_until, $1) END,
				temp_unschedulable_reason = CASE WHEN $1::timestamptz IS NULL THEN a.temp_unschedulable_reason ELSE $2 END,
				updated_at = NOW()
			WHERE a.id = $3 AND a.deleted_at IS NULL
				AND a.platform = $4 AND a.type = $5 AND a.status = $6
				AND a.credentials = $7::jsonb AND a.proxy_id IS NOT DISTINCT FROM $8
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $9, updated.id, NULL, NULL FROM updated`,
		cooldown, reason, id, service.PlatformKiro, service.AccountTypeOAuth, service.StatusActive,
		string(before), proxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 {
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
	}
	return count > 0, nil
}
