//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroOAuthHandlerStub struct {
	kiroOAuthAPI
	calls int
}

func (s *kiroOAuthHandlerStub) Start(_ context.Context, owner int64, in service.KiroOAuthStartInput) (*service.KiroOAuthResult, error) {
	s.calls++
	return &service.KiroOAuthResult{SessionID: "session", Status: "waiting", AccountID: owner}, nil
}
func (s *kiroOAuthHandlerStub) Complete(_ context.Context, _ int64, _, callback string) (*service.KiroOAuthResult, error) {
	s.calls++
	return nil, infraerrors.New(400, "KIRO_OAUTH_INVALID_CALLBACK", "Invalid Kiro callback")
}
func (s *kiroOAuthHandlerStub) Status(_ context.Context, owner int64, _ string) (*service.KiroOAuthResult, error) {
	s.calls++
	return &service.KiroOAuthResult{Status: "waiting", RetryAfter: 5, AccountID: owner}, nil
}

func TestKiroOAuthHandlerOwnerStrictBodiesAndSafeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, route, body string
		owner             int64
		scoped            bool
		status, calls     int
	}{
		{"owner", "/sessions", `{"intent":"create","auth_type":"social"}`, 7, false, 200, 1},
		{"no-owner", "/sessions", `{}`, 0, false, 403, 0},
		{"scoped-key", "/sessions", `{}`, 7, true, 403, 0},
		{"unknown-override", "/sessions", `{"token_endpoint":"secret-sentinel"}`, 7, false, 400, 0},
		{"nested-override", "/sessions", `{"account":{"name":"Kiro","platform":"openai"}}`, 7, false, 400, 0},
		{"malformed", "/sessions", `{"callback_url":"secret-sentinel"`, 7, false, 400, 0},
		{"trailing-json", "/sessions", `{} {}`, 7, false, 400, 0},
		{"complete", "/sessions/id/complete", `{"callback_url":"secret-sentinel"}`, 7, false, 400, 1},
		{"complete-no-proxy", "/sessions/id/complete", `{"callback_url":"secret-sentinel","proxy_id":8}`, 7, false, 400, 0},
		{"action-body", "/sessions/id/poll", `{"deviceCode":"secret-sentinel"}`, 7, false, 400, 0},
		{"action-empty", "/sessions/id/poll", "", 7, false, 200, 1},
		{"finalize-body", "/sessions/id/finalize", `{}`, 7, false, 400, 0},
		{"finalize-empty", "/sessions/id/finalize", "", 7, false, 200, 1},
		{"oversize", "/sessions", `{"credentials":{"refresh_token":"` + strings.Repeat("s", 1<<20) + `"}}`, 7, false, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &kiroOAuthHandlerStub{}
			handler := &KiroOAuthHandler{service: stub}
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				if tc.owner > 0 {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.owner})
				}
				if tc.scoped {
					c.Request = c.Request.WithContext(adminaccess.WithPrincipal(c.Request.Context(), &adminaccess.Principal{}))
				}
				c.Next()
			})
			engine.POST("/sessions", handler.Start)
			engine.POST("/sessions/:id/complete", handler.Complete)
			engine.POST("/sessions/:id/poll", func(c *gin.Context) { handler.action(c, stub.Status) })
			engine.POST("/sessions/:id/finalize", func(c *gin.Context) { handler.action(c, stub.Status) })
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, tc.route, strings.NewReader(tc.body)))
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, tc.calls, stub.calls)
			require.NotContains(t, recorder.Body.String(), "secret-sentinel")
			if tc.status == 200 {
				require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
				if strings.HasSuffix(tc.route, "/poll") {
					require.Equal(t, "5", recorder.Header().Get("Retry-After"))
				}
			}
		})
	}
}
