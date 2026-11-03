package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

type KiroService struct {
	accountRepo  AccountRepository
	httpUpstream HTTPUpstream
	refreshAPI   *OAuthRefreshAPI
}

type KiroOAuthRefreshPersistence interface {
	UpdateKiroOAuthCredentialsIfUnchanged(context.Context, int64, map[string]any, *int64, map[string]any) (bool, error)
	SetKiroOAuthRefreshFailureIfUnchanged(context.Context, int64, map[string]any, *int64, *time.Time, string) (bool, error)
}

func NewKiroService(accountRepo AccountRepository, httpUpstream HTTPUpstream, refreshAPI *OAuthRefreshAPI) *KiroService {
	return &KiroService{accountRepo: accountRepo, httpUpstream: httpUpstream, refreshAPI: refreshAPI}
}

func (s *KiroService) CacheKey(account *Account) string {
	return "kiro:account:" + strconv.FormatInt(account.ID, 10)
}

func (s *KiroService) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformKiro && account.Type == AccountTypeOAuth &&
		account.GetCredential("refresh_token") != ""
}

func (s *KiroService) NeedsRefresh(account *Account, window time.Duration) bool {
	expires := account.GetCredentialAsTime("expires_at")
	if window < 5*time.Minute {
		window = 5 * time.Minute
	}
	return account.GetCredential("access_token") == "" || expires == nil || time.Until(*expires) <= window
}

func (s *KiroService) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	creds, err := kiro.Parse(account.Credentials, account.Type)
	if err != nil {
		return nil, err
	}
	var target, contentType string
	var body []byte
	switch creds.AuthMethod {
	case "social":
		target = "https://prod." + creds.EffectiveAuthRegion() + ".auth.desktop.kiro.dev/refreshToken"
		contentType = "application/json"
		body, err = json.Marshal(map[string]string{"refreshToken": creds.RefreshToken})
	case "idc":
		target = "https://oidc." + creds.EffectiveAuthRegion() + ".amazonaws.com/token"
		contentType = "application/json"
		body, err = json.Marshal(map[string]string{"clientId": creds.ClientID, "clientSecret": creds.ClientSecret,
			"refreshToken": creds.RefreshToken, "grantType": "refresh_token"})
	case "external_idp":
		target, contentType = creds.TokenEndpoint, "application/x-www-form-urlencoded"
		form := url.Values{"client_id": {creds.ClientID}, "refresh_token": {creds.RefreshToken}, "grant_type": {"refresh_token"}}
		if creds.Scopes != "" {
			form.Set("scope", creds.Scopes)
		}
		body = []byte(form.Encode())
	default:
		return nil, errors.New("Kiro credential does not support refresh")
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "KiroIDE-"+firstNonEmpty(creds.KiroVersion, kiro.DefaultVersion)+"-"+creds.Machine())
	resp, err := s.do(req, account)
	if err != nil {
		return nil, err
	}
	data, err := readKiroBody(resp, 512<<10)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamErr := newKiroHTTPError(resp, data)
		if strings.Contains(string(data), "invalid_grant") {
			return nil, fmt.Errorf("Kiro invalid_grant: reauthorize this account")
		}
		return nil, upstreamErr
	}
	var payload struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresIn        int64  `json:"expiresIn"`
		ProfileARN       string `json:"profileArn"`
		OIDCAccessToken  string `json:"access_token"`
		OIDCRefreshToken string `json:"refresh_token"`
		OIDCExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil, errors.New("invalid Kiro token response")
	}
	access := firstNonEmpty(payload.AccessToken, payload.OIDCAccessToken)
	expires := payload.ExpiresIn
	if payload.OIDCExpiresIn > expires {
		expires = payload.OIDCExpiresIn
	}
	if access == "" || expires <= 0 || expires > int64((365*24*time.Hour)/time.Second) {
		return nil, errors.New("Kiro refresh response is missing a valid access token lifetime")
	}
	updated := MergeCredentials(account.Credentials, map[string]any{
		"access_token": access, "expires_at": time.Now().Add(time.Duration(expires) * time.Second).UTC().Format(time.RFC3339),
	})
	if refresh := firstNonEmpty(payload.RefreshToken, payload.OIDCRefreshToken); refresh != "" {
		updated["refresh_token"] = refresh
	}
	if payload.ProfileARN != "" {
		updated["profile_arn"] = payload.ProfileARN
	}
	return updated, nil
}

type kiroForcedRefresh struct {
	*KiroService
	rejectedToken string
}

func (r kiroForcedRefresh) NeedsRefresh(account *Account, window time.Duration) bool {
	return account.GetCredential("access_token") == r.rejectedToken || r.KiroService.NeedsRefresh(account, window)
}

func (s *KiroService) AccessToken(ctx context.Context, account *Account, rejectedToken string) (*Account, string, error) {
	if s == nil || account == nil || account.Platform != PlatformKiro {
		return nil, "", errors.New("Kiro service/account is not configured")
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return nil, "", errors.New("Kiro account's configured proxy is unavailable")
	}
	if s.accountRepo != nil && account.ID > 0 {
		fresh, err := s.accountRepo.GetByID(ctx, account.ID)
		if err != nil {
			return nil, "", err
		}
		if fresh == nil || fresh.Platform != PlatformKiro || fresh.Type != account.Type || fresh.Status != StatusActive {
			return nil, "", errors.New("Kiro account changed or is inactive")
		}
		account = fresh
	}
	if account.Type == AccountTypeAPIKey {
		creds, err := kiro.Parse(account.Credentials, account.Type)
		return account, creds.APIKey, err
	}
	if account.Type != AccountTypeOAuth {
		return nil, "", errors.New("unsupported Kiro account type")
	}
	var executor OAuthRefreshExecutor = s
	if rejectedToken != "" {
		executor = kiroForcedRefresh{KiroService: s, rejectedToken: rejectedToken}
	}
	if executor.NeedsRefresh(account, 5*time.Minute) {
		if s.refreshAPI == nil {
			return nil, "", errors.New("Kiro token refresher is not configured")
		}
		refreshCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		result, err := s.refreshAPI.RefreshIfNeeded(refreshCtx, account, executor, 5*time.Minute)
		if err != nil {
			return nil, "", err
		}
		if result != nil && result.LockHeld {
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-refreshCtx.Done():
					return nil, "", refreshCtx.Err()
				case <-deadline.C:
					return nil, "", errors.New("Kiro token refresh is in progress; retry shortly")
				case <-ticker.C:
					fresh, err := s.accountRepo.GetByID(refreshCtx, account.ID)
					if err != nil {
						return nil, "", err
					}
					if fresh == nil || fresh.Platform != PlatformKiro || fresh.Status != StatusActive {
						return nil, "", errors.New("Kiro account is no longer active")
					}
					if !executor.NeedsRefresh(fresh, 5*time.Minute) {
						account = fresh
						goto refreshed
					}
				}
			}
		}
		if result != nil && result.Account != nil {
			account = result.Account
		}
	}
refreshed:
	if account.Platform != PlatformKiro || account.Type != AccountTypeOAuth || account.Status != StatusActive {
		return nil, "", errors.New("Kiro account changed or is inactive after refresh")
	}
	expires := account.GetCredentialAsTime("expires_at")
	token := account.GetCredential("access_token")
	if token == "" || expires == nil || !time.Now().Before(*expires) {
		return nil, "", errors.New("Kiro access token is unavailable or expired")
	}
	return account, token, nil
}

func (s *KiroService) do(req *http.Request, account *Account) (*http.Response, error) {
	if s.httpUpstream == nil {
		return nil, errors.New("Kiro HTTP transport is not configured")
	}
	proxy := ""
	if account.ProxyID != nil {
		if account.Proxy == nil {
			return nil, errors.New("Kiro account's configured proxy is unavailable")
		}
		proxy = account.Proxy.URL()
	}
	return s.httpUpstream.Do(req, proxy, account.ID, account.Concurrency)
}

type kiroHTTPError struct {
	Status int
	Header http.Header
	Body   []byte
	Reason string
}

func (e *kiroHTTPError) Error() string {
	return fmt.Sprintf("Kiro upstream returned HTTP %d (%s)", e.Status, e.Reason)
}

func newKiroHTTPError(resp *http.Response, data []byte) *kiroHTTPError {
	var payload struct {
		Reason string `json:"reason"`
		Error  struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &payload)
	return &kiroHTTPError{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: data,
		Reason: firstNonEmpty(payload.Reason, payload.Error.Reason)}
}

func readKiroBody(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("Kiro response body exceeds size limit")
	}
	return data, nil
}

func kiroRESTRegions(creds kiro.Credentials) []string {
	if strings.HasPrefix(creds.EffectiveAuthRegion(), "eu-") {
		return []string{"eu-central-1", "us-east-1"}
	}
	return []string{"us-east-1", "eu-central-1"}
}

func (s *KiroService) rest(ctx context.Context, account *Account, token, action string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	creds, err := kiro.Parse(account.Credentials, account.Type)
	if err != nil {
		return nil, err
	}
	creds, err = s.resolveProfile(ctx, account, creds, token)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, region := range kiroRESTRegions(creds) {
		profiles := []string{""}
		if creds.ProfileARN != "" && creds.ProfileARN != kiro.BuilderProfileARN {
			profiles = []string{creds.ProfileARN, ""}
		}
		for _, profile := range profiles {
			query := url.Values{"origin": {"AI_EDITOR"}}
			if action == "getUsageLimits" {
				query.Set("resourceType", "AGENTIC_REQUEST")
				query.Set("isEmailRequired", "true")
			}
			if profile != "" {
				query.Set("profileArn", profile)
			}
			target := "https://q." + region + ".amazonaws.com/" + action + "?" + query.Encode()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
			if err != nil {
				return nil, err
			}
			creds.Decorate(req, token, true)
			resp, err := s.do(req, account)
			if err != nil {
				return nil, err
			}
			data, err := readKiroBody(resp, 8<<20)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if !json.Valid(data) {
					return nil, errors.New("invalid JSON in Kiro REST response")
				}
				return data, nil
			}
			lastErr = newKiroHTTPError(resp, data)
			retryWithoutARN := profile != "" && resp.StatusCode == 400 &&
				(bytes.Contains(data, []byte("Invalid profileArn")) || bytes.Contains(data, []byte("Improperly formed request")))
			if resp.StatusCode != 403 && !retryWithoutARN {
				return nil, lastErr
			}
		}
	}
	return nil, lastErr
}

func (s *KiroService) resolveProfile(ctx context.Context, account *Account, creds kiro.Credentials, token string) (kiro.Credentials, error) {
	if creds.AuthMethod == "api_key" || (creds.ProfileARN != "" && creds.ProfileARN != kiro.BuilderProfileARN) ||
		creds.AuthMethod == "social" {
		return creds, nil
	}
	credentialsJSON, err := json.Marshal(account.Credentials)
	if err != nil {
		return creds, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(credentialsJSON))
	// Cache discovery, not credentials; reauthorization invalidates both positive and negative results.
	if account.GetExtraString("kiro_profile_credentials_hash") == fingerprint {
		checked := account.GetExtraString("kiro_profile_checked_at")
		if at, err := time.Parse(time.RFC3339, checked); err == nil && !at.After(time.Now()) && time.Since(at) < time.Hour {
			creds.ProfileARN = account.GetExtraString("kiro_profile_arn")
			return creds, nil
		}
	}
	cacheProfile := func(arn string) {
		if s.accountRepo != nil && account.ID > 0 {
			_ = s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
				"kiro_profile_checked_at":       time.Now().UTC().Format(time.RFC3339),
				"kiro_profile_credentials_hash": fingerprint, "kiro_profile_arn": arn,
			})
		}
	}
	profileCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastErr error
	empty := false
	for _, region := range kiroRESTRegions(creds) {
		req, err := http.NewRequestWithContext(profileCtx, http.MethodPost,
			"https://q."+region+".amazonaws.com/", strings.NewReader(`{"maxResults":10}`))
		if err != nil {
			return creds, err
		}
		creds.Decorate(req, token, true)
		req.Header.Set("Content-Type", "application/x-amz-json-1.0")
		req.Header.Set("x-amz-target", "AmazonCodeWhispererService.ListAvailableProfiles")
		resp, err := s.do(req, account)
		if err != nil {
			return creds, err
		}
		data, err := readKiroBody(resp, 1<<20)
		if err != nil {
			return creds, err
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var payload struct {
				Profiles []struct {
					ARN string `json:"arn"`
				} `json:"profiles"`
			}
			if err := json.Unmarshal(data, &payload); err != nil {
				return creds, err
			}
			for _, profile := range payload.Profiles {
				if strings.TrimSpace(profile.ARN) != "" {
					creds.ProfileARN = profile.ARN
					cacheProfile(profile.ARN)
					return creds, nil
				}
			}
			empty = true
		} else {
			lastErr = newKiroHTTPError(resp, data)
			if resp.StatusCode == 403 && bytes.Contains(data, []byte("AWS Builder ID is not supported")) {
				empty = true
				break
			}
			if resp.StatusCode != 403 {
				return creds, lastErr
			}
		}
	}
	if empty {
		cacheProfile("")
		return creds, nil
	}
	return creds, lastErr
}

func (s *KiroService) Models(ctx context.Context, account *Account) ([]string, []byte, error) {
	account, token, err := s.AccessToken(ctx, account, "")
	if err != nil {
		return nil, nil, err
	}
	data, err := s.rest(ctx, account, token, "ListAvailableModels")
	if err != nil {
		return nil, nil, err
	}
	var payload struct {
		Models []struct {
			ID          string `json:"modelId"`
			Name        string `json:"modelName"`
			Description string `json:"description"`
			Limits      struct {
				Input  int64 `json:"maxInputTokens"`
				Output int64 `json:"maxOutputTokens"`
			} `json:"tokenLimits"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil, nil, errors.New("invalid Kiro model list")
	}
	ids := make([]string, 0, len(payload.Models))
	metadata := make(map[string]UpstreamModelMetadata)
	for _, model := range payload.Models {
		if _, err := kiro.ModelID(model.ID); err != nil {
			continue
		}
		ids = append(ids, model.ID)
		metadata[model.ID] = UpstreamModelMetadata{ID: model.ID, DisplayName: model.Name,
			Description: model.Description, ContextWindow: model.Limits.Input, MaxOutputTokens: model.Limits.Output}
	}
	if len(ids) == 0 {
		return nil, nil, errors.New("Kiro returned no available models")
	}
	// Adapt only this provider's envelope to the existing admin metadata extractor.
	entries := make([]UpstreamModelMetadata, 0, len(ids))
	for _, id := range dedupeAndSortModelIDs(ids) {
		entries = append(entries, metadata[id])
	}
	catalog, err := json.Marshal(map[string]any{"data": entries})
	return dedupeAndSortModelIDs(ids), catalog, err
}

func (s *KiroService) Usage(ctx context.Context, account *Account) (*UsageInfo, error) {
	account, token, err := s.AccessToken(ctx, account, "")
	if err != nil {
		return nil, err
	}
	data, err := s.rest(ctx, account, token, "getUsageLimits")
	if err != nil {
		return nil, err
	}
	return parseKiroUsage(data, time.Now())
}
