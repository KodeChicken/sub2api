//go:build unit

package kiro

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKiroOAuthPortalAndCallback(t *testing.T) {
	secret, err := OAuthRandom()
	require.NoError(t, err)
	require.Len(t, secret, 43)
	portal, err := url.Parse(PortalURL("state", secret))
	require.NoError(t, err)
	require.Equal(t, "https://app.kiro.dev/signin", portal.Scheme+"://"+portal.Host+portal.Path)
	hash := sha256.Sum256([]byte(secret))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(hash[:]), portal.Query().Get("code_challenge"))
	require.Equal(t, OAuthRedirectBase, portal.Query().Get("redirect_uri"))
	require.Equal(t, "S256", portal.Query().Get("code_challenge_method"))
	require.NotContains(t, portal.RawQuery, secret)
	for _, path := range []string{"/oauth/callback", "/signin/callback"} {
		callback, err := ParseOAuthCallback(OAuthRedirectBase + path + "?code=one%2Btwo&state=state&login_option=GitHub%2BGoogle")
		require.NoError(t, err)
		require.Equal(t, "one+two", callback.Code)
		require.Equal(t, OAuthRedirectBase+path+"?login_option=GitHub%2BGoogle", callback.RedirectURI)
		require.NotContains(t, callback.RedirectURI, "state")
	}
	denied, err := ParseOAuthCallback(OAuthRedirectBase + "/oauth/callback?error=access_denied&state=state")
	require.NoError(t, err)
	require.Equal(t, "access_denied", denied.Error)
}

func TestKiroOAuthCallbackRejectsAmbiguity(t *testing.T) {
	valid := OAuthRedirectBase + "/oauth/callback?code=c&state=s"
	for _, raw := range []string{
		"c", "http://localhost:3128/oauth/callback?code=c&state=s",
		strings.Replace(valid, ":3128", ":3129", 1), strings.Replace(valid, "http:", "https:", 1),
		strings.Replace(valid, "127.0.0.1", "user@127.0.0.1", 1),
		strings.Replace(valid, "/oauth/callback", "/oauth/%63allback", 1),
		strings.Replace(valid, "/oauth/callback", "/oauth/../oauth/callback", 1),
		strings.Replace(valid, "/oauth/callback", "/oauth/callback/", 1),
		valid + "#", valid + "#fragment", valid + "&state=s", valid + "&%73tate=s",
		valid + "&code=c", valid + "&login_option=a&login_option=b",
		valid + "&error=access_denied", valid + "&error_description=secret",
		OAuthRedirectBase + "/oauth/callback?code=&error=access_denied&state=s",
		OAuthRedirectBase + "/oauth/callback?code=c&state=",
		OAuthRedirectBase + "/oauth/callback?code=%20&state=s",
		valid + "&x=%ZZ", valid + "&x=%0aheader", valid + "&login_option=" + strings.Repeat("x", 129),
		valid + "\r\n", valid + strings.Repeat("x", 8192),
	} {
		_, err := ParseOAuthCallback(raw)
		require.Error(t, err, raw)
		require.Equal(t, "paste a complete matching Kiro callback URL", err.Error())
	}
}

func TestKiroOAuthFixedEndpointsAndURLValidation(t *testing.T) {
	host, err := AuthEndpoint("us-east-1")
	require.NoError(t, err)
	require.Equal(t, "https://prod.us-east-1.auth.desktop.kiro.dev", host)
	host, err = OIDCEndpoint("eu-west-1")
	require.NoError(t, err)
	require.Equal(t, "https://oidc.eu-west-1.amazonaws.com", host)
	for _, region := range []string{"", "../evil", "us-east-1.evil", "us-east-1:443", "US-east-1", "us-east-" + strings.Repeat("1", 65)} {
		_, err := OIDCEndpoint(region)
		require.Error(t, err)
	}
	require.NoError(t, ValidateStartURL(BuilderStartURL))
	require.NoError(t, ValidateStartURL("https://example.awsapps.com/start"))
	for _, raw := range []string{"http://example.com/start", "https://user@example.com/start",
		"https://example.com/start?code=secret", "https://example.com/start#fragment", "https://example.com/\n"} {
		require.Error(t, ValidateStartURL(raw))
	}
	require.NoError(t, ValidateVerificationURL("https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD"))
	require.Error(t, ValidateVerificationURL("javascript:alert(1)"))
}

func TestKiroOAuthTokenRequiresFreshCompleteTuple(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	for _, token := range []OAuthToken{
		{AccessToken: "a", RefreshToken: "r", ExpiresIn: 3600},
		{AccessToken: "a", RefreshToken: "r", ExpiresAt: now.Add(time.Hour).Format(time.RFC3339)},
	} {
		credentials, err := token.Credentials(now)
		require.NoError(t, err)
		require.Equal(t, "r", credentials["refresh_token"])
		require.Equal(t, now.Add(time.Hour).Format(time.RFC3339), credentials["expires_at"])
	}
	for _, token := range []OAuthToken{
		{AccessToken: "a", ExpiresIn: 3600}, {RefreshToken: "r", ExpiresIn: 3600},
		{AccessToken: "a", RefreshToken: "r"}, {AccessToken: "a", RefreshToken: "r", ExpiresIn: -1},
		{AccessToken: "a", RefreshToken: "r", ExpiresIn: 366 * 86400},
		{AccessToken: "a", RefreshToken: "r", ExpiresAt: now.Format(time.RFC3339)},
		{AccessToken: "a", RefreshToken: "r", ExpiresAt: "invalid"},
		{AccessToken: "a\nsecret", RefreshToken: "r", ExpiresIn: 3600},
	} {
		_, err := token.Credentials(now)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestKiroOAuthRedisCASAndClaimDeadline(t *testing.T) {
	server := miniredis.RunT(t)
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	server.SetTime(now)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	first, second := NewOAuthSessionStore(client), NewOAuthSessionStore(client)
	ctx := context.Background()
	raw, clock, err := first.Read(ctx, "session")
	require.NoError(t, err)
	require.Empty(t, raw)
	require.True(t, now.Equal(clock))
	ok, err := first.Swap(ctx, "session", nil, []byte("waiting"), now.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = second.Swap(ctx, "session", nil, []byte("other"), now.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = second.Swap(ctx, "session", []byte("waiting"), []byte("cancelled"), time.Time{}, time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = first.Swap(ctx, "session", []byte("waiting"), []byte("ready"), now.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "late publication cannot resurrect a cancelled session")
	server.SetTime(now.Add(2 * time.Minute))
	ok, err = first.Swap(ctx, "session", []byte("cancelled"), []byte("ready"), now.Add(time.Minute), time.Minute)
	require.NoError(t, err)
	require.False(t, ok, "Redis clock fences a late claim even if bytes still match")
	_, _, err = NewOAuthSessionStore(nil).Read(ctx, "session")
	require.Error(t, err, "there is no in-memory fallback")
}
