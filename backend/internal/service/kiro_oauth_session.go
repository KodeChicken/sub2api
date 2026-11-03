package service

import (
	"context"
	"encoding/json"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

const kiroOAuthTTL = 10 * time.Minute

type kiroOAuthSession struct {
	ID              string
	OwnerID         int64
	Intent          string
	AuthType        string
	Status          string
	ErrorCode       string
	Revision        int
	Attempt         string
	ClaimUntil      time.Time
	ExpiresAt       time.Time
	ProxyID         *int64
	ProxyURL        string
	AccountID       int64
	Expected        map[string]any
	Draft           *KiroOAuthAccountInput
	Settings        map[string]any
	Credentials     map[string]any
	State           string
	Verifier        string
	CallbackHash    string
	AuthRegion      string
	StartURL        string
	ClientID        string
	ClientSecret    string
	ClientIssuedAt  int64
	ClientExpiresAt int64
	DeviceCode      string
	UserCode        string
	Verification    string
	VerificationAll string
	Interval        int64
	NextPollAt      time.Time
	Failures        int
	Binding         string
	Raw             []byte    `json:"-"`
	Now             time.Time `json:"-"`
}

type KiroOAuthResult struct {
	SessionID               string `json:"session_id"`
	AuthType                string `json:"auth_type,omitempty"`
	Status                  string `json:"status"`
	ErrorCode               string `json:"error_code,omitempty"`
	ExpiresAt               string `json:"expires_at,omitempty"`
	AuthURL                 string `json:"auth_url,omitempty"`
	UserCode                string `json:"user_code,omitempty"`
	VerificationURI         string `json:"verification_uri,omitempty"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	PollInterval            int64  `json:"poll_interval,omitempty"`
	RetryAfter              int64  `json:"retry_after,omitempty"`
	AccountID               int64  `json:"account_id,omitempty"`
}

func kiroOAuthError(status int, code string) error {
	return infraerrors.New(status, "KIRO_OAUTH_"+code, "Kiro authorization: "+code)
}

func (s *KiroOAuthService) readSession(ctx context.Context, owner int64, id string) (*kiroOAuthSession, error) {
	if owner <= 0 || len(id) != 43 {
		return nil, kiroOAuthError(404, "SESSION_NOT_FOUND")
	}
	raw, now, err := s.sessions.Read(ctx, id)
	if err != nil {
		return nil, kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
	}
	var session kiroOAuthSession
	if len(raw) == 0 || json.Unmarshal(raw, &session) != nil || session.OwnerID != owner || session.ID != id {
		return nil, kiroOAuthError(404, "SESSION_NOT_FOUND")
	}
	session.Raw, session.Now = raw, now
	return &session, nil
}

func (s *KiroOAuthService) swapSession(ctx context.Context, session *kiroOAuthSession, deadline time.Time) error {
	session.Revision++
	raw, err := json.Marshal(session)
	if err != nil {
		return kiroOAuthError(500, "SESSION_INVALID")
	}
	retainUntil := session.ExpiresAt
	if session.Status == "saving" && session.ClaimUntil.After(retainUntil) {
		retainUntil = session.ClaimUntil
	}
	ttl := retainUntil.Sub(session.Now) + 5*time.Second
	if ttl < time.Second {
		ttl = time.Second
	}
	ok, err := s.sessions.Swap(ctx, session.ID, session.Raw, raw, deadline, ttl)
	if err != nil {
		return kiroOAuthError(503, "SESSION_STORE_UNAVAILABLE")
	}
	if !ok {
		return kiroOAuthError(409, "SESSION_CHANGED")
	}
	session.Raw = raw
	return nil
}

func (s *KiroOAuthService) claimSession(ctx context.Context, session *kiroOAuthSession, phase string, lease time.Duration) error {
	attempt, err := kiro.OAuthRandom()
	if err != nil {
		return kiroOAuthError(500, "RANDOM_FAILED")
	}
	session.Status, session.Attempt = phase, attempt
	session.ErrorCode = ""
	session.ClaimUntil = session.Now.Add(lease)
	return s.swapSession(ctx, session, session.ExpiresAt)
}

func (session *kiroOAuthSession) eraseSecrets() {
	session.Credentials, session.Expected = nil, nil
	session.State, session.Verifier, session.ClientSecret, session.DeviceCode, session.ProxyURL = "", "", "", "", ""
}

func (session *kiroOAuthSession) finish(status, reason string) {
	session.Status, session.ErrorCode = status, reason
	session.Attempt = ""
	session.ClaimUntil = time.Time{}
	session.eraseSecrets()
}

func (session *kiroOAuthSession) result() *KiroOAuthResult {
	result := &KiroOAuthResult{SessionID: session.ID, AuthType: session.AuthType, Status: session.Status,
		ErrorCode: session.ErrorCode, ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339), PollInterval: session.Interval}
	if session.Status == "waiting" {
		if session.AuthType == "social" {
			result.AuthURL = kiro.PortalURL(session.State, session.Verifier)
		} else {
			result.UserCode, result.VerificationURI = session.UserCode, session.Verification
			result.VerificationURIComplete = session.VerificationAll
			result.RetryAfter = int64(math.Ceil(session.NextPollAt.Sub(session.Now).Seconds()))
			if result.RetryAfter < 0 {
				result.RetryAfter = 0
			}
		}
	}
	if session.Status == "succeeded" {
		result.AccountID = session.AccountID
	}
	if !session.ClaimUntil.IsZero() {
		result.RetryAfter = int64(math.Ceil(session.ClaimUntil.Sub(session.Now).Seconds()))
		if result.RetryAfter < 1 {
			result.RetryAfter = 1
		}
	}
	return result
}

func (s *KiroOAuthService) currentSession(ctx context.Context, owner int64, id string, cancel bool) (*kiroOAuthSession, *KiroOAuthResult, error) {
	if receipt, err := s.committedKiroOAuth(ctx, owner, id); err != nil || receipt != nil {
		return nil, receipt, err
	}
	session, err := s.readSession(ctx, owner, id)
	if err != nil {
		return nil, nil, err
	}
	if session.Status == "saving" {
		if session.Now.Before(session.ClaimUntil) {
			return session, nil, nil
		}
		result, err := s.persistKiroOAuth(ctx, session, false, cancel)
		return nil, result, err
	}
	if session.Status == "succeeded" || session.Status == "cancelled" || session.Status == "denied" ||
		session.Status == "expired" || session.Status == "failed" {
		return session, nil, nil
	}
	if cancel {
		session.finish("cancelled", "")
	} else if !session.Now.Before(session.ExpiresAt) {
		session.finish("expired", "EXPIRED")
	} else if !session.ClaimUntil.IsZero() && !session.Now.Before(session.ClaimUntil) {
		code := "UPSTREAM_RESULT_UNKNOWN"
		if session.Status == "starting" {
			code = "RESTART_REQUIRED"
		}
		session.finish("failed", code)
	} else {
		return session, nil, nil
	}
	if err := s.swapSession(ctx, session, time.Time{}); err != nil {
		return nil, nil, err
	}
	return session, nil, nil
}
