// Package kiro implements Kiro's native protocol, without a separate proxy process.
package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	DefaultRegion     = "us-east-1"
	DefaultVersion    = "2.3.0"
	UsageVersion      = "0.9.2"
	BuilderProfileARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
	SocialProfileARN  = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
)

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+){1,2}-[0-9]+$`)

type Credentials struct {
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	APIKey        string `json:"api_key"`
	AuthMethod    string `json:"auth_method"`
	Provider      string `json:"provider"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	ProfileARN    string `json:"profile_arn"`
	Region        string `json:"region"`
	AuthRegion    string `json:"auth_region"`
	APIRegion     string `json:"api_region"`
	MachineID     string `json:"machine_id"`
	Endpoint      string `json:"endpoint"`
	APIDomain     string `json:"api_domain"`
	KiroVersion   string `json:"kiro_version"`
	TokenEndpoint string `json:"token_endpoint"`
	Scopes        string `json:"scopes"`
}

// Normalize accepts both Sub2API credentials and kiro.rs/IDE credential exports.
func Normalize(input map[string]any, accountType string) (map[string]any, error) {
	out := Canonicalize(input)
	data, err := json.Marshal(out)
	if err != nil {
		return nil, errors.New("invalid Kiro credentials")
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errors.New("Kiro credential fields must be strings")
	}
	return validateCredentials(out, c, accountType)
}

func Canonicalize(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	for old, key := range map[string]string{
		"accessToken": "access_token", "refreshToken": "refresh_token",
		"expiresAt": "expires_at", "expiresIn": "expires_in", "profileArn": "profile_arn",
		"authMethod": "auth_method", "clientId": "client_id",
		"clientIdIssuedAt": "client_id_issued_at", "clientSecretExpiresAt": "client_secret_expires_at",
		"clientSecret": "client_secret", "authRegion": "auth_region",
		"apiRegion": "api_region", "machineId": "machine_id",
		"kiroApiKey": "api_key", "kiro_api_key": "api_key",
		"apiDomain": "api_domain", "kiroVersion": "kiro_version",
		"tokenEndpoint": "token_endpoint", "startUrl": "start_url",
		"issuerUrl": "issuer_url",
	} {
		if _, exists := out[key]; !exists {
			if value, ok := out[old]; ok {
				out[key] = value
			}
		}
		delete(out, old)
	}
	return out
}

func validateCredentials(out map[string]any, c Credentials, accountType string) (map[string]any, error) {
	c.AuthMethod = strings.ToLower(strings.TrimSpace(c.AuthMethod))
	switch c.AuthMethod {
	case "builderid", "builder_id", "enterprise", "sso", "iam_sso":
		c.AuthMethod = "idc"
	case "github", "google":
		c.AuthMethod = "social"
	case "azure", "azuread", "entra", "entra_id":
		c.AuthMethod = "external_idp"
	case "apikey":
		c.AuthMethod = "api_key"
	}
	if accountType == "apikey" {
		c.AuthMethod = "api_key"
		if !strings.HasPrefix(c.APIKey, "ksk_") {
			return nil, errors.New("Kiro API key must start with ksk_")
		}
	} else if accountType == "oauth" {
		if c.AuthMethod == "" {
			c.AuthMethod = "social"
			if c.ClientID != "" || strings.EqualFold(c.Provider, "BuilderId") {
				c.AuthMethod = "idc"
			}
		}
		if strings.TrimSpace(c.RefreshToken) == "" {
			return nil, errors.New("Kiro OAuth requires refresh_token")
		}
		switch c.AuthMethod {
		case "social":
		case "idc":
			if c.ClientID == "" || c.ClientSecret == "" {
				return nil, errors.New("Kiro IdC requires client_id and client_secret")
			}
		case "external_idp":
			if c.ClientID == "" {
				return nil, errors.New("Kiro enterprise SSO requires client_id")
			}
			u, err := url.Parse(c.TokenEndpoint)
			// Only the supported Entra token authority may receive enterprise refresh tokens.
			if err != nil || u.Scheme != "https" || u.Host != "login.microsoftonline.com" ||
				u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
				!strings.HasSuffix(u.Path, "/oauth2/v2.0/token") {
				return nil, errors.New("Kiro enterprise SSO requires an HTTPS login.microsoftonline.com tenant token endpoint")
			}
		default:
			return nil, errors.New("unsupported Kiro auth_method")
		}
	} else {
		return nil, errors.New("Kiro supports only oauth and apikey accounts")
	}
	if c.Endpoint == "" {
		c.Endpoint = "ide"
		if accountType == "apikey" {
			c.Endpoint = "cli"
		}
	}
	if c.Endpoint != "ide" && c.Endpoint != "cli" {
		return nil, errors.New("Kiro endpoint must be ide or cli")
	}
	if c.APIDomain == "" {
		c.APIDomain = "q"
	}
	if c.APIDomain != "q" && c.APIDomain != "runtime" {
		return nil, errors.New("Kiro api_domain must be q or runtime")
	}
	for _, region := range []string{c.Region, c.AuthRegion, c.APIRegion} {
		if region != "" && !regionPattern.MatchString(region) {
			return nil, errors.New("invalid Kiro region")
		}
	}
	for _, value := range []string{c.AccessToken, c.RefreshToken, c.APIKey, c.ClientID,
		c.ClientSecret, c.MachineID, c.ProfileARN, c.KiroVersion} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return nil, errors.New("invalid control character in Kiro credentials")
		}
	}
	out["auth_method"], out["endpoint"], out["api_domain"] = c.AuthMethod, c.Endpoint, c.APIDomain
	return out, nil
}

func Parse(input map[string]any, accountType string) (Credentials, error) {
	normalized, err := Normalize(input, accountType)
	if err != nil {
		return Credentials{}, err
	}
	data, _ := json.Marshal(normalized)
	var c Credentials
	err = json.Unmarshal(data, &c)
	return c, err
}

func (c Credentials) EffectiveAuthRegion() string {
	return first(c.AuthRegion, c.Region, DefaultRegion)
}

func (c Credentials) EffectiveAPIRegion() string {
	return first(c.APIRegion, c.Region, DefaultRegion)
}

func (c Credentials) RuntimeHost() string {
	if c.APIDomain == "runtime" {
		return "runtime." + c.EffectiveAPIRegion() + ".kiro.dev"
	}
	return "q." + c.EffectiveAPIRegion() + ".amazonaws.com"
}

func (c Credentials) StreamingProfile() string {
	if c.AuthMethod == "api_key" {
		return ""
	}
	if c.ProfileARN != "" {
		return c.ProfileARN
	}
	if c.AuthMethod == "social" && !strings.EqualFold(c.Provider, "BuilderId") {
		return SocialProfileARN
	}
	return BuilderProfileARN
}

func (c Credentials) Machine() string {
	if c.MachineID != "" {
		return c.MachineID
	}
	sum := sha256.Sum256([]byte(first(c.RefreshToken, c.APIKey, c.AccessToken)))
	return hex.EncodeToString(sum[:])
}

func (c Credentials) Decorate(req *http.Request, token string, usage bool) {
	version := first(c.KiroVersion, DefaultVersion)
	if usage {
		version = UsageVersion
	}
	identity := fmt.Sprintf("KiroIDE-%s-%s", version, c.Machine())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-amz-user-agent", "aws-sdk-js/1.0.34 "+identity)
	req.Header.Set("User-Agent", "aws-sdk-js/1.0.34 ua/2.1 os/linux lang/js md/nodejs#22.13.0 api/codewhispererstreaming#1.0.34 "+identity)
	req.Header.Set("amz-sdk-invocation-id", uuid.NewString())
	req.Header.Set("amz-sdk-request", "attempt=1; max=1")
	req.Header.Set("x-amzn-codewhisperer-optout", "true")
	if c.AuthMethod == "api_key" {
		req.Header.Set("tokentype", "API_KEY")
	} else if c.AuthMethod == "external_idp" {
		req.Header.Set("tokentype", "EXTERNAL_IDP")
	}
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
