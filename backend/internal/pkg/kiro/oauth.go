package kiro

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	OAuthRedirectBase = "http://127.0.0.1:3128"
	BuilderStartURL   = "https://view.awsapps.com/start"
	DeviceGrant       = "urn:ietf:params:oauth:grant-type:device_code"
)

var OAuthScopes = []string{
	"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations",
	"codewhisperer:transformations", "codewhisperer:taskassist",
}

func OAuthRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("cannot generate Kiro authorization secret")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func PortalURL(state, verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"}, "redirect_uri": {OAuthRedirectBase}, "redirect_from": {"KiroIDE"},
	}
	return "https://app.kiro.dev/signin?" + q.Encode()
}

func AuthEndpoint(region string) (string, error) {
	if len(region) > 64 || !regionPattern.MatchString(region) {
		return "", errors.New("invalid Kiro authorization region")
	}
	return "https://prod." + region + ".auth.desktop.kiro.dev", nil
}

func OIDCEndpoint(region string) (string, error) {
	if _, err := AuthEndpoint(region); err != nil {
		return "", err
	}
	return "https://oidc." + region + ".amazonaws.com", nil
}

func ValidateStartURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || hasControl(raw) || u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return errors.New("invalid HTTPS SSO Start URL")
	}
	return nil
}

func ValidateVerificationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || hasControl(raw) || u.Scheme != "https" || u.Host == "" ||
		u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return errors.New("invalid Kiro verification URL")
	}
	return nil
}

type OAuthCallback struct {
	Code        string
	State       string
	RedirectURI string
	Error       string
}

func ParseOAuthCallback(raw string) (*OAuthCallback, error) {
	invalid := errors.New("paste a complete matching Kiro callback URL")
	if len(raw) > 8192 || hasControl(raw) {
		return nil, invalid
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host != "127.0.0.1:3128" || u.User != nil ||
		u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" || u.RawPath != "" ||
		(u.Path != "/oauth/callback" && u.Path != "/signin/callback") {
		return nil, invalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, invalid
	}
	for k, values := range q {
		if len(values) != 1 || hasControl(k) || hasControl(values[0]) {
			return nil, invalid
		}
	}
	state := q.Get("state")
	if strings.TrimSpace(state) == "" || len(state) > 256 || len(q.Get("login_option")) > 128 {
		return nil, invalid
	}
	code, upstreamError := q.Get("code"), q.Get("error")
	if (code == "") == (upstreamError == "") || len(code) > 4096 || len(upstreamError) > 128 ||
		(code != "" && strings.TrimSpace(code) == "") ||
		(q.Has("code") && q.Has("error")) || (q.Has("code") && q.Has("error_description")) {
		return nil, invalid
	}
	redirect := OAuthRedirectBase + u.Path
	if option := q.Get("login_option"); option != "" {
		redirect += "?" + (url.Values{"login_option": {option}}).Encode()
	}
	return &OAuthCallback{Code: code, State: state, RedirectURI: redirect, Error: upstreamError}, nil
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

type OAuthToken struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
	ExpiresAt    string `json:"expiresAt"`
	ProfileARN   string `json:"profileArn"`
}

func (t OAuthToken) Credentials(now time.Time) (map[string]any, error) {
	expires := now.Add(time.Duration(t.ExpiresIn) * time.Second)
	if t.ExpiresAt != "" {
		var err error
		expires, err = time.Parse(time.RFC3339, t.ExpiresAt)
		if err != nil {
			return nil, errors.New("invalid Kiro token lifetime")
		}
	} else if t.ExpiresIn <= 0 || t.ExpiresIn > 365*24*3600 {
		return nil, errors.New("invalid Kiro token lifetime")
	}
	if strings.TrimSpace(t.AccessToken) == "" || strings.TrimSpace(t.RefreshToken) == "" ||
		hasControl(t.AccessToken) || hasControl(t.RefreshToken) || hasControl(t.ProfileARN) ||
		!expires.After(now) || expires.After(now.Add(365*24*time.Hour)) {
		return nil, errors.New("Kiro authorization returned incomplete credentials")
	}
	result := map[string]any{"access_token": t.AccessToken, "refresh_token": t.RefreshToken,
		"expires_at": expires.UTC().Format(time.RFC3339)}
	if t.ProfileARN != "" {
		result["profile_arn"] = t.ProfileARN
	}
	return result, nil
}
