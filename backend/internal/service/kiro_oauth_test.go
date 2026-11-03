//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type kiroOAuthTestAdmin struct {
	AdminService
	create func(context.Context, *CreateAccountInput) (*Account, error)
}

func (*kiroOAuthTestAdmin) ValidateAccountGroupBindings(context.Context, []int64) error {
	return nil
}

func (a *kiroOAuthTestAdmin) CreateAccount(ctx context.Context, input *CreateAccountInput) (*Account, error) {
	return a.create(ctx, input)
}

type kiroOAuthTestProxy struct {
	ProxyRepository
	proxy *Proxy
}

func (p *kiroOAuthTestProxy) GetByID(context.Context, int64) (*Proxy, error) { return p.proxy, nil }

func newKiroOAuthTest(t *testing.T) (*KiroOAuthService, *miniredis.Miniredis, sqlmock.Sqlmock) {
	t.Helper()
	server := miniredis.RunT(t)
	server.SetTime(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = redisClient.Close()
		_ = db.Close()
	})
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	s := NewKiroOAuthService(kiro.NewOAuthSessionStore(redisClient), nil, nil, &kiroOAuthTestAdmin{}, client, nil, nil, nil)
	return s, server, mock
}

func kiroOAuthCreateInput(auth string) KiroOAuthStartInput {
	return KiroOAuthStartInput{Intent: "create", AuthType: auth,
		Account: &KiroOAuthAccountInput{Name: "Kiro", Concurrency: 10, Priority: 1, GroupIDs: []int64{5}}}
}

func kiroOAuthNoReceipt(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT response_body FROM idempotency_records").
		WillReturnRows(sqlmock.NewRows([]string{"response_body"}))
}

func kiroOAuthGuard(mock sqlmock.Sqlmock, session *kiroOAuthSession, status, body string) {
	mock.ExpectBegin()
	for range 3 {
		mock.ExpectExec("SET LOCAL").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec("INSERT INTO idempotency_records").
		WithArgs(kiroOAuthReceiptScope, kiroOAuthReceiptKey(session.OwnerID, session.ID), session.Binding,
			int64(kiroOAuthReceiptTTL()/time.Second), session.ExpiresAt.Add(time.Minute)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT status,COALESCE").
		WithArgs(kiroOAuthReceiptScope, kiroOAuthReceiptKey(session.OwnerID, session.ID)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "response_body", "request_fingerprint"}).
			AddRow(status, body, session.Binding))
}

func TestKiroOAuthSocialStagesOnceAndValidatesState(t *testing.T) {
	s, _, mock := newKiroOAuthTest(t)
	var calls atomic.Int32
	s.upstream = &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Equal(t, "https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token", req.URL.String())
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		var body map[string]string
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, kiro.OAuthRedirectBase+"/signin/callback?login_option=GitHub", body["redirect_uri"])
		require.Equal(t, "code", body["code"])
		require.Len(t, body["code_verifier"], 43)
		require.Len(t, body, 3)
		return kiroTestResponse(200, `{"accessToken":"access-sentinel","refreshToken":"refresh-sentinel","expiresIn":3600}`), nil
	}}
	result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
	require.NoError(t, err)
	portal, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	callback := kiro.OAuthRedirectBase + "/signin/callback?code=code&state=" + portal.Query().Get("state") + "&login_option=GitHub"
	kiroOAuthNoReceipt(mock)
	_, err = s.Complete(context.Background(), 7, result.SessionID, strings.Replace(callback, "state=", "state=wrong", 1))
	require.Equal(t, "KIRO_OAUTH_INVALID_STATE", infraerrors.Reason(err))
	require.Zero(t, calls.Load())
	kiroOAuthNoReceipt(mock)
	_, err = s.Complete(context.Background(), 8, result.SessionID, callback)
	require.Equal(t, 404, infraerrors.Code(err))
	kiroOAuthNoReceipt(mock)
	ready, err := s.Complete(context.Background(), 7, result.SessionID, callback)
	require.NoError(t, err)
	require.Equal(t, "ready", ready.Status)
	encoded, err := json.Marshal(ready)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sentinel")
	kiroOAuthNoReceipt(mock)
	again, err := s.Complete(context.Background(), 7, result.SessionID, callback)
	require.NoError(t, err)
	require.Equal(t, "ready", again.Status)
	require.EqualValues(t, 1, calls.Load())
	kiroOAuthNoReceipt(mock)
	_, err = s.Complete(context.Background(), 7, result.SessionID, strings.Replace(callback, "code=code", "code=other", 1))
	require.Equal(t, "KIRO_OAUTH_CALLBACK_CHANGED", infraerrors.Reason(err))
}

func TestKiroOAuthCancellationFencesLateExchange(t *testing.T) {
	s, _, mock := newKiroOAuthTest(t)
	entered, release := make(chan struct{}), make(chan struct{})
	s.upstream = &kiroTestTransport{call: func(*http.Request) (*http.Response, error) {
		close(entered)
		<-release
		return kiroTestResponse(200, `{"accessToken":"a","refreshToken":"r","expiresIn":3600}`), nil
	}}
	result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
	require.NoError(t, err)
	portal, _ := url.Parse(result.AuthURL)
	callback := kiro.OAuthRedirectBase + "/oauth/callback?code=c&state=" + portal.Query().Get("state")
	kiroOAuthNoReceipt(mock)
	done := make(chan error, 1)
	go func() {
		_, err := s.Complete(context.Background(), 7, result.SessionID, callback)
		done <- err
	}()
	<-entered
	kiroOAuthNoReceipt(mock)
	inProgress, err := s.Complete(context.Background(), 7, result.SessionID, callback)
	require.NoError(t, err)
	require.Equal(t, "exchanging", inProgress.Status, "racing completion must not exchange twice")
	kiroOAuthNoReceipt(mock)
	cancelled, err := s.Cancel(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
	close(release)
	require.Equal(t, 409, infraerrors.Code(<-done))
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Empty(t, session.Credentials)
	require.Empty(t, session.Verifier)
	require.Equal(t, "cancelled", session.Status)
}

func TestKiroOAuthDeviceProtocolAndBackoff(t *testing.T) {
	for _, auth := range []string{"builder_id", "iam_sso"} {
		t.Run(auth, func(t *testing.T) {
			s, server, mock := newKiroOAuthTest(t)
			input := kiroOAuthCreateInput(auth)
			startURL := kiro.BuilderStartURL
			if auth == "iam_sso" {
				startURL = "https://company.awsapps.com/start"
				input.StartURL = startURL
			}
			polls := 0
			s.upstream = &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "oidc.us-east-1.amazonaws.com", req.URL.Host)
				require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
				var body map[string]any
				require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
				switch req.URL.Path {
				case "/client/register":
					require.Equal(t, "public", body["clientType"])
					require.Equal(t, startURL, body["issuerUrl"])
					require.Len(t, body["scopes"], 5)
					require.Equal(t, []any{kiro.DeviceGrant, "refresh_token"}, body["grantTypes"])
					return kiroTestResponse(200, `{"clientId":"client","clientSecret":"secret"}`), nil
				case "/device_authorization":
					require.Equal(t, map[string]any{"clientId": "client", "clientSecret": "secret", "startUrl": startURL}, body)
					return kiroTestResponse(200, `{"deviceCode":"device-secret","userCode":"ABCD","verificationUri":"https://device.example/start","expiresIn":600,"interval":1}`), nil
				default:
					require.Equal(t, "/token", req.URL.Path)
					require.Equal(t, kiro.DeviceGrant, body["grantType"])
					require.Equal(t, "device-secret", body["deviceCode"])
					polls++
					if polls <= 2 {
						return kiroTestResponse(400, `{"error":"slow_down"}`), nil
					}
					if polls == 3 {
						return kiroTestResponse(400, `{"error":"authorization_pending"}`), nil
					}
					return kiroTestResponse(200, `{"accessToken":"a","refreshToken":"r","expiresIn":3600}`), nil
				}
			}}
			result, err := s.Start(context.Background(), 7, input)
			require.NoError(t, err)
			require.EqualValues(t, 5, result.PollInterval)
			require.Equal(t, "ABCD", result.UserCode)
			kiroOAuthNoReceipt(mock)
			_, err = s.Poll(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.Zero(t, polls, "early polling must not reach AWS")
			now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
			for _, step := range []struct {
				advance, interval int64
				status            string
			}{{5, 10, "waiting"}, {10, 15, "waiting"}, {15, 15, "waiting"}, {15, 15, "ready"}} {
				now = now.Add(time.Duration(step.advance) * time.Second)
				server.SetTime(now)
				kiroOAuthNoReceipt(mock)
				result, err = s.Poll(context.Background(), 7, result.SessionID)
				require.NoError(t, err)
				require.Equal(t, step.status, result.Status)
				require.Equal(t, step.interval, result.PollInterval)
			}
			session, err := s.readSession(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.Equal(t, "idc", session.Credentials["auth_method"])
			require.Equal(t, startURL, session.Credentials["start_url"])
			require.Equal(t, "secret", session.Credentials["client_secret"])
		})
	}
}

func TestKiroOAuthStaleClaimsRequireRestart(t *testing.T) {
	for _, phase := range []string{"starting", "exchanging", "polling"} {
		t.Run(phase, func(t *testing.T) {
			s, server, mock := newKiroOAuthTest(t)
			result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
			require.NoError(t, err)
			session, err := s.readSession(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.NoError(t, s.claimSession(context.Background(), session, phase, 30*time.Second))
			late := *session
			server.SetTime(session.Now.Add(31 * time.Second))
			kiroOAuthNoReceipt(mock)
			result, err = s.Status(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			late.Status = "ready"
			_, err = s.publishOAuthSession(context.Background(), &late)
			require.Equal(t, 409, infraerrors.Code(err))
		})
	}
}

func TestKiroOAuthProxyAndTargetBinding(t *testing.T) {
	s, _, mock := newKiroOAuthTest(t)
	proxyID := int64(8)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 9999, Status: StatusActive}
	s.proxyRepo = &kiroOAuthTestProxy{proxy: proxy}
	input := kiroOAuthCreateInput("social")
	input.Account.ProxyID = &proxyID
	result, err := s.Start(context.Background(), 7, input)
	require.NoError(t, err)
	portal, _ := url.Parse(result.AuthURL)
	proxy.Port++
	kiroOAuthNoReceipt(mock)
	result, err = s.Complete(context.Background(), 7, result.SessionID,
		kiro.OAuthRedirectBase+"/oauth/callback?code=c&state="+portal.Query().Get("state"))
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	s.accountRepo = &kiroTestAccounts{account: &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}
	_, err = s.Start(context.Background(), 7, KiroOAuthStartInput{Intent: "reauth", AuthType: "social", AccountID: 42})
	require.Equal(t, "KIRO_OAUTH_INVALID_TARGET", infraerrors.Reason(err))
	input = kiroOAuthCreateInput("social")
	input.Settings = map[string]any{"token_endpoint": "https://evil.example"}
	_, err = s.Start(context.Background(), 7, input)
	require.Equal(t, "KIRO_OAUTH_INVALID_SETTINGS", infraerrors.Reason(err))
}

func TestKiroOAuthSaveRetryAndReceiptReplay(t *testing.T) {
	s, _, mock := newKiroOAuthTest(t)
	input := kiroOAuthCreateInput("credentials")
	input.Credentials = map[string]any{"refresh_token": "fresh", "endpoint": "cli"}
	result, err := s.Start(context.Background(), 7, input)
	require.NoError(t, err)
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	writes := 0
	s.admin = &kiroOAuthTestAdmin{create: func(ctx context.Context, input *CreateAccountInput) (*Account, error) {
		require.NotNil(t, dbent.TxFromContext(ctx), "account and group writes must use the receipt transaction")
		require.Equal(t, "cli", input.Credentials["endpoint"])
		require.Equal(t, []int64{5}, input.GroupIDs)
		writes++
		if writes == 1 {
			return nil, errors.New("simulated group/save failure with token sentinel")
		}
		return &Account{ID: 42}, nil
	}}
	kiroOAuthNoReceipt(mock)
	kiroOAuthGuard(mock, session, "processing", "")
	mock.ExpectRollback()
	_, err = s.Finalize(context.Background(), 7, result.SessionID)
	require.Equal(t, "KIRO_OAUTH_SAVE_FAILED", infraerrors.Reason(err))
	require.NotContains(t, err.Error(), "sentinel")
	session, err = s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, "ready", session.Status)
	require.Equal(t, "fresh", session.Credentials["refresh_token"])
	kiroOAuthNoReceipt(mock)
	kiroOAuthGuard(mock, session, "processing", "")
	mock.ExpectExec("UPDATE idempotency_records SET status='succeeded'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	result, err = s.Finalize(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", result.Status)
	require.EqualValues(t, 42, result.AccountID)
	encoded, _ := json.Marshal(result)
	mock.ExpectQuery("SELECT response_body FROM idempotency_records").
		WillReturnRows(sqlmock.NewRows([]string{"response_body"}).AddRow(string(encoded)))
	replayed, err := s.Finalize(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, result, replayed)
	require.Equal(t, 2, writes, "only one retry, never another mutation after durable success")
	require.NotContains(t, string(encoded), "fresh")
}

func TestKiroOAuthSavingRecoverySerializesAndFencesOldSaver(t *testing.T) {
	for _, cancelSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "cancel"}[cancelSession], func(t *testing.T) {
			s, server, mock := newKiroOAuthTest(t)
			input := kiroOAuthCreateInput("credentials")
			input.Credentials = map[string]any{"refresh_token": "staged"}
			result, err := s.Start(context.Background(), 7, input)
			require.NoError(t, err)
			session, err := s.readSession(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.NoError(t, s.claimSession(context.Background(), session, "saving", 20*time.Second))
			server.SetTime(session.Now.Add(21 * time.Second))
			second := *s
			kiroOAuthNoReceipt(mock)
			kiroOAuthGuard(mock, session, "processing", "")
			mock.ExpectExec("UPDATE idempotency_records SET").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if cancelSession {
				result, err = second.Cancel(context.Background(), 7, session.ID)
				require.Equal(t, "cancelled", result.Status)
			} else {
				result, err = second.Status(context.Background(), 7, session.ID)
				require.Equal(t, "ready", result.Status)
				require.Equal(t, "SAVE_FAILED", result.ErrorCode, "recovery must require an explicit save retry")
			}
			require.NoError(t, err)
			kiroOAuthGuard(mock, session, "processing", "")
			mock.ExpectRollback()
			_, err = s.persistKiroOAuth(context.Background(), session, true, false)
			require.Equal(t, "KIRO_OAUTH_SESSION_CHANGED", infraerrors.Reason(err), "old saver paused before SQL guard cannot write")
		})
	}
}

func TestKiroOAuthReceiptContentionIsUnknownNotCancellation(t *testing.T) {
	s, server, mock := newKiroOAuthTest(t)
	input := kiroOAuthCreateInput("credentials")
	input.Credentials = map[string]any{"refresh_token": "staged"}
	result, err := s.Start(context.Background(), 7, input)
	require.NoError(t, err)
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.NoError(t, s.claimSession(context.Background(), session, "saving", 20*time.Second))
	server.SetTime(session.Now.Add(21 * time.Second))
	kiroOAuthNoReceipt(mock)
	mock.ExpectBegin()
	for range 3 {
		mock.ExpectExec("SET LOCAL").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec("INSERT INTO idempotency_records").WillReturnError(errors.New("lock timeout: prior saver holds unique receipt"))
	mock.ExpectRollback()
	_, err = s.Cancel(context.Background(), 7, result.SessionID)
	require.Equal(t, "KIRO_OAUTH_PERSISTENCE_UNKNOWN", infraerrors.Reason(err))
	session, err = s.readSession(context.Background(), 7, session.ID)
	require.NoError(t, err)
	require.Equal(t, "saving", session.Status)
	require.Equal(t, "staged", session.Credentials["refresh_token"])
}

func TestKiroOAuthReauthReplacesIdentityPreservesConfiguration(t *testing.T) {
	existing := map[string]any{
		"refresh_token": "old", "access_token": "old-access", "auth_method": "idc",
		"client_id": "old-client", "client_secret": "old-secret", "profile_arn": "old-profile",
		"token_endpoint": "old", "provider": "Enterprise", "scopes": "old", "start_url": "old",
		"clientIdIssuedAt": float64(10), "clientSecretExpiresAt": float64(20), "expiresIn": float64(30),
		"auth_region": "eu-west-1", "region": "ap-southeast-1", "api_region": "eu-west-2",
		"endpoint": "cli", "api_domain": "runtime", "machine_id": "machine",
		"kiro_version": "old-version", "model_mapping": map[string]any{"alias": "model"},
		"temp_unschedulable_enabled": true,
	}
	fresh := map[string]any{"refreshToken": "new", "authRegion": "us-east-1"}
	updated, err := replaceKiroOAuthCredentials(existing, fresh)
	require.NoError(t, err)
	require.Equal(t, "social", updated["auth_method"], "inferred fresh method must replace the previous method")
	for _, key := range []string{"client_id", "client_secret", "profile_arn", "token_endpoint", "provider", "scopes", "start_url", "access_token",
		"client_id_issued_at", "client_secret_expires_at", "expires_in", "clientIdIssuedAt", "clientSecretExpiresAt", "expiresIn"} {
		require.NotContains(t, updated, key)
	}
	for _, key := range []string{"endpoint", "api_domain", "api_region", "region", "machine_id", "kiro_version", "model_mapping", "temp_unschedulable_enabled"} {
		require.Equal(t, existing[key], updated[key], key)
	}
	require.Equal(t, "new", updated["refresh_token"])
	legacyRegion, err := replaceKiroOAuthCredentials(existing, map[string]any{"refresh_token": "new", "region": "us-west-2"})
	require.NoError(t, err)
	require.Equal(t, "us-west-2", legacyRegion["auth_region"])
	require.Equal(t, existing["region"], legacyRegion["region"], "fresh auth region must not change inference fallback")
	mixed, err := replaceKiroOAuthCredentials(existing, map[string]any{
		"auth_method": "social", "refresh_token": "new", "provider": "BuilderId",
		"client_id": "unrelated", "client_secret": "unrelated", "token_endpoint": "https://unrelated.example",
	})
	require.NoError(t, err)
	for _, key := range []string{"client_id", "client_secret", "provider", "token_endpoint"} {
		require.NotContains(t, mixed, key, "a fresh social tuple must not retain other methods' metadata")
	}
	require.Equal(t, "old", existing["refresh_token"], "replacement must not mutate the start snapshot")
	for _, invalid := range []map[string]any{
		{"auth_method": "idc", "refresh_token": "new"},
		{"auth_method": "social", "access_token": "new"},
		{"auth_method": "external_idp", "refresh_token": "new", "client_id": "c", "token_endpoint": "https://evil.example"},
	} {
		_, err = replaceKiroOAuthCredentials(existing, invalid)
		require.Error(t, err, "fresh reauth cannot borrow old secrets")
	}
	require.GreaterOrEqual(t, kiroOAuthReceiptTTL(), time.Hour)
}

func TestKiroOAuthCommitUnknownReplaysDurableReceiptAfterRedisLoss(t *testing.T) {
	s, server, mock := newKiroOAuthTest(t)
	input := kiroOAuthCreateInput("credentials")
	input.Credentials = map[string]any{"refresh_token": "staged"}
	result, err := s.Start(context.Background(), 7, input)
	require.NoError(t, err)
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	writes := 0
	s.admin = &kiroOAuthTestAdmin{create: func(ctx context.Context, _ *CreateAccountInput) (*Account, error) {
		require.NotNil(t, dbent.TxFromContext(ctx))
		writes++
		return &Account{ID: 42}, nil
	}}
	kiroOAuthNoReceipt(mock)
	kiroOAuthGuard(mock, session, "processing", "")
	mock.ExpectExec("UPDATE idempotency_records SET status='succeeded'").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("connection lost after commit"))
	_, err = s.Finalize(context.Background(), 7, result.SessionID)
	require.Equal(t, "KIRO_OAUTH_PERSISTENCE_UNKNOWN", infraerrors.Reason(err))
	server.FlushAll()
	success := &KiroOAuthResult{SessionID: result.SessionID, AuthType: "credentials", Status: "succeeded", AccountID: 42}
	body, _ := json.Marshal(success)
	mock.ExpectQuery("SELECT response_body FROM idempotency_records").
		WillReturnRows(sqlmock.NewRows([]string{"response_body"}).AddRow(string(body)))
	replayed, err := s.Status(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, success, replayed)
	require.Equal(t, 1, writes)
	kiroOAuthNoReceipt(mock)
	_, err = s.Finalize(context.Background(), 7, result.SessionID)
	require.Equal(t, 404, infraerrors.Code(err), "a missing receipt/session never authorizes a new write")
	require.Equal(t, 1, writes)
}

func TestKiroOAuthReceiptRetentionFloor(t *testing.T) {
	prior := DefaultIdempotencyCoordinator()
	t.Cleanup(func() { SetDefaultIdempotencyCoordinator(prior) })
	SetDefaultIdempotencyCoordinator(&IdempotencyCoordinator{cfg: IdempotencyConfig{DefaultTTL: time.Second}})
	require.Equal(t, time.Hour, kiroOAuthReceiptTTL())
	require.NotEqual(t, DefaultWriteIdempotencyTTL(), kiroOAuthReceiptTTL())
}

type kiroOAuthTestReauthRepo struct {
	AccountRepository
	account *Account
	applied int
}

func (r *kiroOAuthTestReauthRepo) GetByID(context.Context, int64) (*Account, error) {
	out := *r.account
	out.Credentials = maps.Clone(r.account.Credentials)
	return &out, nil
}

func (r *kiroOAuthTestReauthRepo) ApplyKiroOAuthReauthorization(ctx context.Context, id int64, expected map[string]any, proxy *int64, next map[string]any) (bool, error) {
	if dbent.TxFromContext(ctx) == nil || id != r.account.ID || kiroOAuthHash(expected) != kiroOAuthHash(r.account.Credentials) {
		return false, errors.New("reauth escaped its transaction or target snapshot")
	}
	r.applied++
	r.account.Credentials = maps.Clone(next)
	return true, nil
}

func TestKiroOAuthReauthTargetFenceAndSettings(t *testing.T) {
	for _, change := range []string{"none", "credentials", "proxy", "type"} {
		t.Run(change, func(t *testing.T) {
			s, _, mock := newKiroOAuthTest(t)
			repo := &kiroOAuthTestReauthRepo{account: &Account{ID: 42, Platform: PlatformKiro,
				Type: AccountTypeOAuth, Status: "inactive", Schedulable: false, Concurrency: 17,
				GroupIDs: []int64{5, 6}, Priority: 3, Extra: map[string]any{"config": "preserve"},
				Credentials: map[string]any{"refresh_token": "old", "endpoint": "cli", "api_region": "eu-west-1", "machine_id": "preserve"}}}
			s.accountRepo = repo
			result, err := s.Start(context.Background(), 7, KiroOAuthStartInput{
				Intent: "reauth", AuthType: "credentials", AccountID: 42,
				Credentials: map[string]any{"refreshToken": "new", "auth_method": "social"},
			})
			require.NoError(t, err)
			session, err := s.readSession(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			switch change {
			case "credentials":
				repo.account.Credentials["refresh_token"] = "concurrent-refresh"
			case "proxy":
				id := int64(8)
				repo.account.ProxyID = &id
			case "type":
				repo.account.Type = AccountTypeAPIKey
			}
			kiroOAuthNoReceipt(mock)
			kiroOAuthGuard(mock, session, "processing", "")
			mock.ExpectExec("SELECT id FROM accounts").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
			if change == "none" {
				mock.ExpectExec("UPDATE idempotency_records SET status='succeeded'").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			result, err = s.Finalize(context.Background(), 7, session.ID)
			if change == "none" {
				require.NoError(t, err)
				require.EqualValues(t, 42, result.AccountID)
				require.Equal(t, "new", repo.account.Credentials["refresh_token"])
				require.Equal(t, 1, repo.applied)
			} else {
				require.Equal(t, "KIRO_OAUTH_TARGET_CHANGED", infraerrors.Reason(err))
				require.Zero(t, repo.applied)
				require.NotEqual(t, "new", repo.account.Credentials["refresh_token"])
			}
			require.Equal(t, "cli", repo.account.Credentials["endpoint"])
			require.Equal(t, "eu-west-1", repo.account.Credentials["api_region"])
			require.Equal(t, "preserve", repo.account.Credentials["machine_id"])
			require.Equal(t, "inactive", repo.account.Status)
			require.False(t, repo.account.Schedulable)
			require.Equal(t, 17, repo.account.Concurrency)
			require.Equal(t, []int64{5, 6}, repo.account.GroupIDs)
			require.Equal(t, map[string]any{"config": "preserve"}, repo.account.Extra)
		})
	}
}

func TestKiroOAuthTerminalExpiryAndSafeUpstreamFailure(t *testing.T) {
	s, server, mock := newKiroOAuthTest(t)
	s.upstream = &kiroTestTransport{call: func(*http.Request) (*http.Response, error) {
		return kiroTestResponse(400, `{"error":"sentinel-client-secret","error_description":"sentinel-tokens"}`), nil
	}}
	result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
	require.NoError(t, err)
	portal, _ := url.Parse(result.AuthURL)
	kiroOAuthNoReceipt(mock)
	result, err = s.Complete(context.Background(), 7, result.SessionID,
		kiro.OAuthRedirectBase+"/oauth/callback?code=c&state="+portal.Query().Get("state"))
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	body, _ := json.Marshal(result)
	require.NotContains(t, string(body), "sentinel")
	result, err = s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
	require.NoError(t, err)
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	server.SetTime(session.ExpiresAt)
	kiroOAuthNoReceipt(mock)
	result, err = s.Finalize(context.Background(), 7, session.ID)
	require.NoError(t, err)
	require.Equal(t, "expired", result.Status)
}

func TestKiroOAuthExpiredStagingCannotStartAccountSave(t *testing.T) {
	s, server, mock := newKiroOAuthTest(t)
	result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("social"))
	require.NoError(t, err)
	session, err := s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	session.Status = "ready"
	session.Credentials = map[string]any{"access_token": "a", "refresh_token": "r",
		"expires_at": session.Now.Add(time.Second).Format(time.RFC3339)}
	require.NoError(t, s.swapSession(context.Background(), session, session.ExpiresAt))
	server.SetTime(session.Now.Add(2 * time.Second))
	kiroOAuthNoReceipt(mock)
	result, err = s.Finalize(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Equal(t, "expired", result.Status)
	session, err = s.readSession(context.Background(), 7, result.SessionID)
	require.NoError(t, err)
	require.Empty(t, session.Credentials)
}

func TestKiroOAuthDeviceTerminalStatesAndFailureBound(t *testing.T) {
	for _, tc := range []struct {
		reason, want string
		http, polls  int
	}{
		{"access_denied", "denied", 400, 1},
		{"expired_token", "expired", 400, 1},
		{"unknown", "failed", 400, 1},
		{"unknown", "failed", 503, 5},
	} {
		t.Run(tc.reason+"/"+tc.want, func(t *testing.T) {
			s, server, mock := newKiroOAuthTest(t)
			polls := 0
			s.upstream = &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/client/register":
					return kiroTestResponse(200, `{"clientId":"c","clientSecret":"s"}`), nil
				case "/device_authorization":
					return kiroTestResponse(200, `{"deviceCode":"d","userCode":"ABCD","verificationUri":"https://device.example/start","expiresIn":600,"interval":5}`), nil
				default:
					polls++
					return kiroTestResponse(tc.http, `{"error":"`+tc.reason+`","error_description":"secret-sentinel"}`), nil
				}
			}}
			result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("builder_id"))
			require.NoError(t, err)
			for range tc.polls {
				session, err := s.readSession(context.Background(), 7, result.SessionID)
				require.NoError(t, err)
				server.SetTime(session.NextPollAt)
				kiroOAuthNoReceipt(mock)
				result, err = s.Poll(context.Background(), 7, result.SessionID)
				require.NoError(t, err)
			}
			require.Equal(t, tc.want, result.Status)
			require.Equal(t, tc.polls, polls)
			session, err := s.readSession(context.Background(), 7, result.SessionID)
			require.NoError(t, err)
			require.Empty(t, session.ClientSecret)
			require.Empty(t, session.DeviceCode)
			encoded, _ := json.Marshal(result)
			require.NotContains(t, string(encoded), "sentinel")
		})
	}
}

func TestKiroOAuthRejectsExpiredRegistration(t *testing.T) {
	s, _, _ := newKiroOAuthTest(t)
	s.upstream = &kiroTestTransport{call: func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/client/register" {
			return kiroTestResponse(200, `{"clientId":"c","clientSecret":"s","clientSecretExpiresAt":0}`), nil
		}
		return kiroTestResponse(200, `{"deviceCode":"d","userCode":"ABCD","verificationUri":"https://device.example/start","expiresIn":600,"interval":5}`), nil
	}}
	result, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("builder_id"))
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	invalid, err := s.Start(context.Background(), 7, kiroOAuthCreateInput("credentials"))
	require.NoError(t, err)
	require.Equal(t, "INVALID_CREDENTIALS", invalid.ErrorCode)
}
