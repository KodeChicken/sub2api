package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

type KiroOAuthAccountInput struct {
	Name                    string         `json:"name"`
	Notes                   *string        `json:"notes"`
	ProxyID                 *int64         `json:"proxy_id"`
	Concurrency             int            `json:"concurrency"`
	Priority                int            `json:"priority"`
	RateMultiplier          *float64       `json:"rate_multiplier"`
	LoadFactor              *int           `json:"load_factor"`
	GroupIDs                []int64        `json:"group_ids"`
	ExpiresAt               *int64         `json:"expires_at"`
	AutoPauseOnExpired      *bool          `json:"auto_pause_on_expired"`
	Extra                   map[string]any `json:"extra"`
	ConfirmMixedChannelRisk bool           `json:"confirm_mixed_channel_risk"`
}

type KiroOAuthStartInput struct {
	Intent      string                 `json:"intent"`
	AuthType    string                 `json:"auth_type"`
	AccountID   int64                  `json:"account_id"`
	Account     *KiroOAuthAccountInput `json:"account"`
	AuthRegion  string                 `json:"auth_region"`
	StartURL    string                 `json:"start_url"`
	Settings    map[string]any         `json:"settings"`
	Credentials map[string]any         `json:"credentials"`
}

type KiroOAuthService struct {
	sessions    kiro.OAuthSessionStore
	proxyRepo   ProxyRepository
	accountRepo AccountRepository
	admin       AdminService
	ent         *dbent.Client
	upstream    HTTPUpstream
	invalidator TokenCacheInvalidator
	blocker     AccountRuntimeBlocker
}

func NewKiroOAuthService(store kiro.OAuthSessionStore, proxies ProxyRepository, accounts AccountRepository,
	admin AdminService, ent *dbent.Client, upstream HTTPUpstream, invalidator TokenCacheInvalidator,
	blocker AccountRuntimeBlocker) *KiroOAuthService {
	return &KiroOAuthService{sessions: store, proxyRepo: proxies, accountRepo: accounts, admin: admin,
		ent: ent, upstream: upstream, invalidator: invalidator, blocker: blocker}
}

func kiroOAuthHash(value any) string {
	data, _ := json.Marshal(value)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *KiroOAuthService) proxyURL(ctx context.Context, id *int64) (string, error) {
	if id == nil {
		return "", nil
	}
	if *id <= 0 || s.proxyRepo == nil {
		return "", kiroOAuthError(400, "PROXY_UNAVAILABLE")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *id)
	if err != nil || proxy == nil || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return "", kiroOAuthError(400, "PROXY_UNAVAILABLE")
	}
	return proxy.URL(), nil
}

func (s *KiroOAuthService) validateProxy(ctx context.Context, session *kiroOAuthSession) error {
	proxy, err := s.proxyURL(ctx, session.ProxyID)
	if err != nil || proxy != session.ProxyURL {
		return kiroOAuthError(409, "PROXY_CHANGED")
	}
	return nil
}

var kiroOAuthSettingKeys = map[string]bool{
	"endpoint": true, "api_region": true, "api_domain": true, "machine_id": true,
	"kiro_version": true, "model_mapping": true, "temp_unschedulable_enabled": true, "temp_unschedulable_rules": true,
}

func (s *KiroOAuthService) Start(ctx context.Context, owner int64, input KiroOAuthStartInput) (*KiroOAuthResult, error) {
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer stop()
	if owner <= 0 || (input.Intent != "create" && input.Intent != "reauth") ||
		(input.AuthType != "social" && input.AuthType != "builder_id" && input.AuthType != "iam_sso" && input.AuthType != "credentials") {
		return nil, kiroOAuthError(400, "INVALID_INPUT")
	}
	for key := range input.Settings {
		if !kiroOAuthSettingKeys[key] {
			return nil, kiroOAuthError(400, "INVALID_SETTINGS")
		}
	}
	id, err := kiro.OAuthRandom()
	if err != nil {
		return nil, kiroOAuthError(500, "RANDOM_FAILED")
	}
	_, now, err := s.sessions.Read(ctx, id)
	if err != nil {
		return nil, kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
	}
	session := &kiroOAuthSession{ID: id, OwnerID: owner, Intent: input.Intent, AuthType: input.AuthType,
		Status: "starting", ExpiresAt: now.Add(kiroOAuthTTL), Now: now, Settings: input.Settings,
		AuthRegion: strings.TrimSpace(input.AuthRegion)}
	if session.AuthRegion == "" {
		session.AuthRegion = kiro.DefaultRegion
	}
	if _, err := kiro.AuthEndpoint(session.AuthRegion); err != nil {
		return nil, kiroOAuthError(400, "INVALID_REGION")
	}
	if input.Intent == "create" {
		draft := input.Account
		if draft == nil || input.AccountID != 0 || strings.TrimSpace(draft.Name) == "" ||
			utf8.RuneCountInString(draft.Name) > 100 || draft.Concurrency < 0 || draft.Priority < 0 ||
			(draft.RateMultiplier != nil && *draft.RateMultiplier < 0) ||
			(draft.LoadFactor != nil && (*draft.LoadFactor < 0 || *draft.LoadFactor > 10000)) {
			return nil, kiroOAuthError(400, "INVALID_ACCOUNT")
		}
		if err := s.admin.ValidateAccountGroupBindings(ctx, draft.GroupIDs); err != nil {
			return nil, kiroOAuthError(400, "INVALID_GROUPS")
		}
		session.Draft, session.ProxyID = draft, draft.ProxyID
	} else {
		if input.Account != nil || input.AccountID <= 0 || len(input.Settings) != 0 {
			return nil, kiroOAuthError(400, "INVALID_TARGET")
		}
		account, err := s.accountRepo.GetByID(ctx, input.AccountID)
		if err != nil || account == nil || account.Platform != PlatformKiro || account.Type != AccountTypeOAuth || account.IsShadow() {
			return nil, kiroOAuthError(400, "INVALID_TARGET")
		}
		session.AccountID, session.ProxyID, session.Expected = account.ID, account.ProxyID, account.Credentials
	}
	session.ProxyURL, err = s.proxyURL(ctx, session.ProxyID)
	if err != nil {
		return nil, err
	}
	session.StartURL = kiro.BuilderStartURL
	if input.AuthType == "iam_sso" {
		session.StartURL = strings.TrimSpace(input.StartURL)
		if kiro.ValidateStartURL(session.StartURL) != nil {
			return nil, kiroOAuthError(400, "INVALID_START_URL")
		}
	} else if input.StartURL != "" {
		return nil, kiroOAuthError(400, "INVALID_START_URL")
	}
	session.Binding = kiroOAuthHash([]any{owner, session.Intent, session.AuthType, session.AccountID,
		session.ProxyID, session.ProxyURL, session.Expected, session.Draft, session.Settings, session.AuthRegion, session.StartURL})
	if err := s.claimSession(ctx, session, "starting", 35*time.Second); err != nil {
		return nil, err
	}
	deadline := session.ClaimUntil
	switch input.AuthType {
	case "credentials":
		session.Credentials, err = replaceKiroOAuthCredentials(input.Credentials, input.Credentials)
		if err == nil {
			if expires := credentialTime(session.Credentials["expires_at"]); expires == nil || !expires.After(now) {
				delete(session.Credentials, "access_token")
				delete(session.Credentials, "expires_at")
			}
			session.Status = "ready"
		}
	case "social":
		session.State, err = kiro.OAuthRandom()
		if err == nil {
			session.Verifier, err = kiro.OAuthRandom()
		}
		session.Status = "waiting"
	default:
		err = s.startDevice(ctx, session)
	}
	if err != nil {
		reason := "START_FAILED"
		if input.AuthType == "credentials" {
			reason = "INVALID_CREDENTIALS"
		}
		session.finish("failed", reason)
	}
	session.ClaimUntil = time.Time{}
	if session.Status == "waiting" || session.Status == "ready" {
		deadline = minTime(deadline, session.ExpiresAt)
	}
	if err := s.swapSession(ctx, session, deadline); err != nil {
		return nil, err
	}
	return session.result(), nil
}

func credentialTime(value any) *time.Time {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil
	}
	return &at
}

func (s *KiroOAuthService) oauthPost(ctx context.Context, session *kiroOAuthSession, target string, input, output any) (int, error) {
	if err := s.validateProxy(ctx, session); err != nil {
		return 0, err
	}
	if s.upstream == nil {
		return 0, kiroOAuthError(503, "TRANSPORT_UNAVAILABLE")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return 0, kiroOAuthError(400, "INVALID_INPUT")
	}
	ctx, cancel := context.WithTimeout(WithHTTPUpstreamRedirectsDisabled(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, kiroOAuthError(400, "INVALID_ENDPOINT")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "KiroIDE-"+kiro.DefaultVersion)
	resp, err := s.upstream.Do(req, session.ProxyURL, session.AccountID, 1)
	if err != nil {
		return 0, kiroOAuthError(502, "UPSTREAM_RESULT_UNKNOWN")
	}
	data, err := readKiroBody(resp, 512<<10)
	if err != nil {
		return resp.StatusCode, kiroOAuthError(502, "UPSTREAM_RESULT_UNKNOWN")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var upstreamError struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &upstreamError)
		switch upstreamError.Error {
		case "authorization_pending", "slow_down", "access_denied", "expired_token":
			return resp.StatusCode, errors.New(upstreamError.Error)
		default:
			return resp.StatusCode, kiroOAuthError(502, "UPSTREAM_REJECTED")
		}
	}
	if json.Unmarshal(data, output) != nil {
		return resp.StatusCode, kiroOAuthError(502, "UPSTREAM_RESULT_UNKNOWN")
	}
	return resp.StatusCode, nil
}

func (s *KiroOAuthService) startDevice(ctx context.Context, session *kiroOAuthSession) error {
	host, _ := kiro.OIDCEndpoint(session.AuthRegion)
	var registration struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
		IssuedAt     int64  `json:"clientIdIssuedAt"`
		ExpiresAt    *int64 `json:"clientSecretExpiresAt"`
	}
	_, err := s.oauthPost(ctx, session, host+"/client/register", map[string]any{
		"clientName": "kiro-rs", "clientType": "public", "scopes": kiro.OAuthScopes,
		"grantTypes": []string{kiro.DeviceGrant, "refresh_token"}, "issuerUrl": session.StartURL,
	}, &registration)
	if err != nil || registration.ClientID == "" || registration.ClientSecret == "" {
		return kiroOAuthError(502, "REGISTRATION_FAILED")
	}
	session.ClientID, session.ClientSecret = registration.ClientID, registration.ClientSecret
	session.ClientIssuedAt = registration.IssuedAt
	if registration.ExpiresAt != nil {
		session.ClientExpiresAt = *registration.ExpiresAt
	}
	var device struct {
		DeviceCode  string `json:"deviceCode"`
		UserCode    string `json:"userCode"`
		URI         string `json:"verificationUri"`
		CompleteURI string `json:"verificationUriComplete"`
		ExpiresIn   int64  `json:"expiresIn"`
		Interval    int64  `json:"interval"`
	}
	_, err = s.oauthPost(ctx, session, host+"/device_authorization", map[string]string{
		"clientId": session.ClientID, "clientSecret": session.ClientSecret, "startUrl": session.StartURL,
	}, &device)
	if err != nil || device.DeviceCode == "" || device.UserCode == "" || len(device.UserCode) > 128 ||
		device.ExpiresIn <= 0 || device.ExpiresIn > 86400 || device.Interval < 0 || device.Interval > 600 ||
		kiro.ValidateVerificationURL(device.URI) != nil ||
		(device.CompleteURI != "" && kiro.ValidateVerificationURL(device.CompleteURI) != nil) {
		return kiroOAuthError(502, "DEVICE_START_FAILED")
	}
	_, now, err := s.sessions.Read(ctx, session.ID)
	if err != nil {
		return kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
	}
	session.Now = now
	session.ExpiresAt = minTime(session.ExpiresAt, session.Now.Add(time.Duration(device.ExpiresIn)*time.Second))
	if registration.ExpiresAt != nil {
		session.ExpiresAt = minTime(session.ExpiresAt, time.Unix(*registration.ExpiresAt, 0))
	}
	if !session.ExpiresAt.After(session.Now) {
		return kiroOAuthError(502, "REGISTRATION_EXPIRED")
	}
	session.DeviceCode, session.UserCode = device.DeviceCode, device.UserCode
	session.Verification, session.VerificationAll = device.URI, device.CompleteURI
	session.Interval = device.Interval
	if session.Interval < 5 {
		session.Interval = 5
	}
	session.NextPollAt = session.Now.Add(time.Duration(session.Interval) * time.Second)
	session.Status = "waiting"
	return nil
}

func (s *KiroOAuthService) Status(ctx context.Context, owner int64, id string) (*KiroOAuthResult, error) {
	session, result, err := s.currentSession(ctx, owner, id, false)
	if err != nil || result != nil {
		return result, err
	}
	return session.result(), nil
}

func (s *KiroOAuthService) Cancel(ctx context.Context, owner int64, id string) (*KiroOAuthResult, error) {
	session, result, err := s.currentSession(ctx, owner, id, true)
	if err != nil || result != nil {
		return result, err
	}
	if session.Status == "saving" {
		return nil, kiroOAuthError(409, "SAVE_IN_PROGRESS")
	}
	return session.result(), nil
}

func (s *KiroOAuthService) Complete(ctx context.Context, owner int64, id, rawCallback string) (*KiroOAuthResult, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	session, result, err := s.currentSession(ctx, owner, id, false)
	if err != nil || result != nil {
		return result, err
	}
	if session.AuthType != "social" {
		return nil, kiroOAuthError(400, "WRONG_MODE")
	}
	if session.Status == "expired" || session.Status == "failed" || session.Status == "denied" || session.Status == "cancelled" {
		return session.result(), nil
	}
	callback, err := kiro.ParseOAuthCallback(rawCallback)
	if err != nil {
		return nil, kiroOAuthError(400, "INVALID_CALLBACK")
	}
	if subtle.ConstantTimeCompare([]byte(callback.State), []byte(session.State)) != 1 {
		return nil, kiroOAuthError(400, "INVALID_STATE")
	}
	hash := kiroOAuthHash(rawCallback)
	if session.Status != "waiting" {
		if session.CallbackHash != "" && session.CallbackHash != hash {
			return nil, kiroOAuthError(409, "CALLBACK_CHANGED")
		}
		return session.result(), nil
	}
	if callback.Error != "" {
		status := "failed"
		if callback.Error == "access_denied" {
			status = "denied"
		}
		session.finish(status, "UPSTREAM_REJECTED")
		if err := s.swapSession(ctx, session, session.ExpiresAt); err != nil {
			return nil, err
		}
		return session.result(), nil
	}
	session.CallbackHash = hash
	if err := s.claimSession(ctx, session, "exchanging", 30*time.Second); err != nil {
		return nil, err
	}
	host, _ := kiro.AuthEndpoint(session.AuthRegion)
	var token kiro.OAuthToken
	_, err = s.oauthPost(ctx, session, host+"/oauth/token", map[string]string{
		"code": callback.Code, "code_verifier": session.Verifier, "redirect_uri": callback.RedirectURI,
	}, &token)
	return s.stageOAuthToken(ctx, session, token, err)
}

func (s *KiroOAuthService) Poll(ctx context.Context, owner int64, id string) (*KiroOAuthResult, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	session, result, err := s.currentSession(ctx, owner, id, false)
	if err != nil || result != nil {
		return result, err
	}
	if session.AuthType != "builder_id" && session.AuthType != "iam_sso" {
		return nil, kiroOAuthError(400, "WRONG_MODE")
	}
	if session.Status != "waiting" || session.Now.Before(session.NextPollAt) {
		return session.result(), nil
	}
	if err := s.claimSession(ctx, session, "polling", 30*time.Second); err != nil {
		return nil, err
	}
	host, _ := kiro.OIDCEndpoint(session.AuthRegion)
	var token kiro.OAuthToken
	status, err := s.oauthPost(ctx, session, host+"/token", map[string]string{
		"clientId": session.ClientID, "clientSecret": session.ClientSecret,
		"deviceCode": session.DeviceCode, "grantType": kiro.DeviceGrant,
	}, &token)
	if err == nil {
		return s.stageOAuthToken(ctx, session, token, nil)
	}
	switch err.Error() {
	case "authorization_pending", "slow_down":
		if err.Error() == "slow_down" {
			session.Interval += 5
		}
		session.Status, session.Failures = "waiting", 0
	case "access_denied":
		session.finish("denied", "ACCESS_DENIED")
	case "expired_token":
		session.finish("expired", "EXPIRED")
	default:
		if status >= 500 && session.Failures < 4 {
			session.Failures++
			session.Status = "waiting"
		} else {
			session.finish("failed", "UPSTREAM_RESULT_UNKNOWN")
		}
	}
	return s.publishOAuthSession(ctx, session)
}

func (s *KiroOAuthService) stageOAuthToken(ctx context.Context, session *kiroOAuthSession, token kiro.OAuthToken, upstreamErr error) (*KiroOAuthResult, error) {
	var err error
	if upstreamErr == nil {
		_, session.Now, err = s.sessions.Read(ctx, session.ID)
		if err != nil {
			return nil, kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
		}
		session.Credentials, err = token.Credentials(session.Now)
	} else {
		err = upstreamErr
	}
	if err != nil {
		session.finish("failed", "UPSTREAM_RESULT_UNKNOWN")
	} else {
		session.Credentials["auth_region"] = session.AuthRegion
		session.Credentials["auth_method"] = "social"
		if session.AuthType != "social" {
			session.Credentials["auth_method"] = "idc"
			session.Credentials["client_id"], session.Credentials["client_secret"] = session.ClientID, session.ClientSecret
			session.Credentials["start_url"], session.Credentials["issuer_url"] = session.StartURL, session.StartURL
			session.Credentials["provider"] = "BuilderId"
			if session.ClientIssuedAt > 0 {
				session.Credentials["client_id_issued_at"] = session.ClientIssuedAt
			}
			if session.ClientExpiresAt > 0 {
				session.Credentials["client_secret_expires_at"] = session.ClientExpiresAt
			}
			if session.AuthType == "iam_sso" {
				session.Credentials["provider"] = "Enterprise"
			}
		}
		_, err = kiro.Normalize(session.Credentials, AccountTypeOAuth)
		if err != nil {
			session.finish("failed", "UPSTREAM_RESULT_UNKNOWN")
		} else {
			session.Status = "ready"
		}
	}
	return s.publishOAuthSession(ctx, session)
}

func (s *KiroOAuthService) publishOAuthSession(ctx context.Context, session *kiroOAuthSession) (*KiroOAuthResult, error) {
	// Keep the original bytes/attempt as the fence; a cancelled or reclaimed attempt cannot publish.
	deadline := session.ClaimUntil
	if deadline.IsZero() {
		var original kiroOAuthSession
		_ = json.Unmarshal(session.Raw, &original)
		deadline = original.ClaimUntil
	}
	_, now, err := s.sessions.Read(ctx, session.ID)
	if err != nil {
		return nil, kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
	}
	session.Now = now
	session.NextPollAt = now.Add(time.Duration(session.Interval) * time.Second * time.Duration(max(1, session.Failures)))
	session.ClaimUntil = time.Time{}
	if !now.Before(session.ExpiresAt) {
		session.finish("expired", "EXPIRED")
	} else {
		deadline = minTime(deadline, session.ExpiresAt)
	}
	if err := s.swapSession(ctx, session, deadline); err != nil {
		return nil, err
	}
	return session.result(), nil
}
