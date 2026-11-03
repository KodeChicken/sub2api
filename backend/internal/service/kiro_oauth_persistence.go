package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const kiroOAuthReceiptScope = "admin.kiro.oauth.finalize"

type KiroOAuthReauthPersistence interface {
	ApplyKiroOAuthReauthorization(context.Context, int64, map[string]any, *int64, map[string]any) (bool, error)
}

func kiroOAuthReceiptKey(owner int64, id string) string {
	return HashIdempotencyKey(fmt.Sprintf("%d:%s", owner, id))
}

func (s *KiroOAuthService) committedKiroOAuth(ctx context.Context, owner int64, id string) (*KiroOAuthResult, error) {
	if owner <= 0 || len(id) != 43 {
		return nil, kiroOAuthError(404, "SESSION_NOT_FOUND")
	}
	if s.ent == nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := s.ent.QueryContext(ctx, `SELECT response_body FROM idempotency_records
		WHERE scope = $1 AND idempotency_key_hash = $2 AND status = 'succeeded' AND expires_at > NOW()`,
		kiroOAuthReceiptScope, kiroOAuthReceiptKey(owner, id))
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
		return nil, nil
	}
	var body string
	var result KiroOAuthResult
	if rows.Scan(&body) != nil || json.Unmarshal([]byte(body), &result) != nil || result.Status != "succeeded" {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	return &result, nil
}

func (s *KiroOAuthService) Finalize(ctx context.Context, owner int64, id string) (*KiroOAuthResult, error) {
	session, result, err := s.currentSession(ctx, owner, id, false)
	if err != nil || result != nil {
		return result, err
	}
	if session.Status != "ready" {
		return session.result(), nil
	}
	if session.AuthType != "credentials" {
		expiry := credentialTime(session.Credentials["expires_at"])
		if expiry == nil || !expiry.After(session.Now) {
			session.finish("expired", "EXPIRED")
			if err := s.swapSession(ctx, session, time.Time{}); err != nil {
				return nil, err
			}
			return session.result(), nil
		}
	}
	if err := s.validateProxy(ctx, session); err != nil {
		return nil, err
	}
	if err := s.claimSession(ctx, session, "saving", 20*time.Second); err != nil {
		return nil, err
	}
	return s.persistKiroOAuth(ctx, session, true, false)
}

func kiroOAuthReceiptTTL() time.Duration {
	ttl := DefaultWriteIdempotencyTTL()
	if ttl < time.Hour {
		ttl = time.Hour
	}
	return ttl
}

func (s *KiroOAuthService) persistKiroOAuth(ctx context.Context, session *kiroOAuthSession, mutate, cancelSession bool) (*KiroOAuthResult, error) {
	budget := 3 * time.Second
	if mutate {
		budget = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()
	if s.ent == nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	tx, err := s.ent.Tx(ctx)
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	defer func() { _ = tx.Rollback() }()
	client := tx.Client()
	for _, command := range []string{
		"SET LOCAL lock_timeout = '1s'", "SET LOCAL statement_timeout = '8s'",
		"SET LOCAL idle_in_transaction_session_timeout = '10s'",
	} {
		if _, err := client.ExecContext(ctx, command); err != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
	}
	key := kiroOAuthReceiptKey(session.OwnerID, session.ID)
	_, err = client.ExecContext(ctx, `INSERT INTO idempotency_records
		(scope,idempotency_key_hash,request_fingerprint,status,expires_at)
		VALUES ($1,$2,$3,'processing',GREATEST(NOW() + $4 * INTERVAL '1 second',$5))
		ON CONFLICT (scope,idempotency_key_hash) DO NOTHING`,
		kiroOAuthReceiptScope, key, session.Binding, int64(kiroOAuthReceiptTTL()/time.Second), session.ExpiresAt.Add(time.Minute))
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	rows, err := client.QueryContext(ctx, `SELECT status,COALESCE(response_body,''),request_fingerprint
		FROM idempotency_records WHERE scope=$1 AND idempotency_key_hash=$2 FOR UPDATE`, kiroOAuthReceiptScope, key)
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	var status, body, binding string
	if !rows.Next() || rows.Scan(&status, &body, &binding) != nil {
		_ = rows.Close()
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	_ = rows.Close()
	if binding != session.Binding {
		return nil, kiroOAuthError(409, "SESSION_CHANGED")
	}
	if status == "succeeded" {
		var result KiroOAuthResult
		if json.Unmarshal([]byte(body), &result) != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
		return &result, nil
	}
	current, err := s.readSession(ctx, session.OwnerID, session.ID)
	if err != nil || current.Status != "saving" || current.Attempt != session.Attempt {
		return nil, kiroOAuthError(409, "SESSION_CHANGED")
	}
	if !mutate {
		if current.Now.Before(current.ClaimUntil) {
			return current.result(), nil
		}
		current.Status, current.Attempt = "ready", ""
		current.ErrorCode = "SAVE_FAILED"
		current.ClaimUntil = time.Time{}
		if cancelSession {
			current.finish("cancelled", "")
		} else if !current.Now.Before(current.ExpiresAt) {
			current.finish("expired", "EXPIRED")
		}
		if err := s.swapSession(ctx, current, time.Time{}); err != nil {
			return nil, err
		}
		if _, err := client.ExecContext(ctx, `UPDATE idempotency_records SET
			expires_at=GREATEST(expires_at,NOW() + $3 * INTERVAL '1 second',$4),updated_at=NOW()
			WHERE scope=$1 AND idempotency_key_hash=$2`, kiroOAuthReceiptScope, key,
			int64(kiroOAuthReceiptTTL()/time.Second), current.ExpiresAt.Add(time.Minute)); err != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
		if err := tx.Commit(); err != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
		return current.result(), nil
	}
	if !current.Now.Before(current.ClaimUntil) {
		return nil, kiroOAuthError(409, "SAVE_IN_PROGRESS")
	}
	txctx := dbent.NewTxContext(ctx, tx)
	account, saveErr := s.saveKiroOAuthAccount(txctx, current)
	if saveErr != nil {
		if err := tx.Rollback(); err != nil {
			return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
		}
		resetCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if latest, err := s.readSession(resetCtx, current.OwnerID, current.ID); err == nil &&
			latest.Attempt == current.Attempt && latest.Status == "saving" {
			latest.Status, latest.Attempt, latest.ErrorCode = "ready", "", "SAVE_FAILED"
			latest.ClaimUntil = time.Time{}
			if !latest.Now.Before(latest.ExpiresAt) {
				latest.finish("expired", "EXPIRED")
			}
			_ = s.swapSession(resetCtx, latest, time.Time{})
		}
		return nil, saveErr
	}
	result := &KiroOAuthResult{SessionID: current.ID, AuthType: current.AuthType, Status: "succeeded", AccountID: account.ID}
	encoded, _ := json.Marshal(result)
	_, err = client.ExecContext(ctx, `UPDATE idempotency_records SET status='succeeded',
		response_status=200,response_body=$3,locked_until=NULL,updated_at=NOW(),
		expires_at=GREATEST(NOW() + $4 * INTERVAL '1 second',$5)
		WHERE scope=$1 AND idempotency_key_hash=$2`, kiroOAuthReceiptScope, key, string(encoded),
		int64(kiroOAuthReceiptTTL()/time.Second), current.ExpiresAt.Add(time.Minute))
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	AfterAccountCommit(txctx, func(committed context.Context) {
		if s.invalidator != nil {
			if err := s.invalidator.InvalidateToken(committed, account); err != nil {
				slog.Warn("kiro_oauth_postcommit_cache_invalidation_failed", "account_id", account.ID)
			}
		}
		if s.blocker != nil && current.Intent == "reauth" {
			s.blocker.ClearAccountSchedulingBlock(account.ID)
		}
	})
	if err := tx.Commit(); err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	current.AccountID = account.ID
	current.finish("succeeded", "")
	postCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer stop()
	_ = s.swapSession(postCtx, current, time.Time{})
	return result, nil
}

func (s *KiroOAuthService) saveKiroOAuthAccount(ctx context.Context, session *kiroOAuthSession) (*Account, error) {
	if err := s.validateProxy(ctx, session); err != nil {
		return nil, err
	}
	if session.Intent == "create" {
		d := session.Draft
		credentials := MergeCredentials(session.Settings, session.Credentials)
		account, err := s.admin.CreateAccount(ctx, &CreateAccountInput{
			Name: d.Name, Notes: d.Notes, Platform: PlatformKiro, Type: AccountTypeOAuth,
			Credentials: credentials, Extra: d.Extra, ProxyID: session.ProxyID, Concurrency: d.Concurrency,
			Priority: d.Priority, RateMultiplier: d.RateMultiplier, LoadFactor: d.LoadFactor, GroupIDs: d.GroupIDs,
			ExpiresAt: d.ExpiresAt, AutoPauseOnExpired: d.AutoPauseOnExpired, SkipMixedChannelCheck: d.ConfirmMixedChannelRisk,
		})
		if err != nil {
			return nil, kiroOAuthError(400, "SAVE_FAILED")
		}
		return account, nil
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNAVAILABLE")
	}
	if _, err := tx.Client().ExecContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, session.AccountID); err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	account, err := s.accountRepo.GetByID(ctx, session.AccountID)
	if err != nil || account == nil || account.Platform != PlatformKiro || account.Type != AccountTypeOAuth || account.IsShadow() ||
		!reflect.DeepEqual(account.ProxyID, session.ProxyID) || kiroOAuthHash(account.Credentials) != kiroOAuthHash(session.Expected) {
		return nil, kiroOAuthError(409, "TARGET_CHANGED")
	}
	credentials, err := replaceKiroOAuthCredentials(account.Credentials, session.Credentials)
	if err != nil {
		return nil, kiroOAuthError(400, "INVALID_CREDENTIALS")
	}
	writer, ok := s.accountRepo.(KiroOAuthReauthPersistence)
	if !ok {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNAVAILABLE")
	}
	applied, err := writer.ApplyKiroOAuthReauthorization(ctx, account.ID, session.Expected, session.ProxyID, credentials)
	if err != nil {
		return nil, kiroOAuthError(503, "PERSISTENCE_UNKNOWN")
	}
	if !applied {
		return nil, kiroOAuthError(409, "TARGET_CHANGED")
	}
	account.Credentials = credentials
	return account, nil
}

var kiroAuthOwnedKeys = []string{
	"access_token", "refresh_token", "expires_at", "expires_in", "auth_method", "provider", "api_key",
	"client_id", "client_secret", "client_id_issued_at", "client_secret_expires_at",
	"auth_region", "start_url", "issuer_url", "token_endpoint", "scopes", "profile_arn",
}

func replaceKiroOAuthCredentials(existing, fresh map[string]any) (map[string]any, error) {
	config := kiro.Canonicalize(existing)
	incoming, err := kiro.Normalize(fresh, AccountTypeOAuth)
	if err != nil {
		return nil, err
	}
	region, _ := incoming["auth_region"].(string)
	if region == "" {
		region, _ = incoming["region"].(string)
	}
	if region == "" {
		region = kiro.DefaultRegion
	}
	incoming["auth_region"] = region
	for _, key := range kiroAuthOwnedKeys {
		delete(config, key)
	}
	keys := []string{"access_token", "refresh_token", "expires_at", "expires_in", "auth_method", "auth_region", "profile_arn"}
	switch incoming["auth_method"] {
	case "idc":
		keys = append(keys, "client_id", "client_secret", "client_id_issued_at", "client_secret_expires_at",
			"provider", "start_url", "issuer_url", "scopes")
	case "external_idp":
		keys = append(keys, "client_id", "client_secret", "provider", "token_endpoint", "scopes")
	}
	for _, key := range keys {
		if value, ok := incoming[key]; ok {
			config[key] = value
		}
	}
	return kiro.Normalize(config, AccountTypeOAuth)
}
