package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type kiroOAuthAPI interface {
	Start(context.Context, int64, service.KiroOAuthStartInput) (*service.KiroOAuthResult, error)
	Complete(context.Context, int64, string, string) (*service.KiroOAuthResult, error)
	Poll(context.Context, int64, string) (*service.KiroOAuthResult, error)
	Finalize(context.Context, int64, string) (*service.KiroOAuthResult, error)
	Status(context.Context, int64, string) (*service.KiroOAuthResult, error)
	Cancel(context.Context, int64, string) (*service.KiroOAuthResult, error)
}

type KiroOAuthHandler struct{ service kiroOAuthAPI }

func NewKiroOAuthHandler(s *service.KiroOAuthService) *KiroOAuthHandler {
	return &KiroOAuthHandler{service: s}
}

func kiroOAuthOwner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 || adminaccess.FromContext(c.Request.Context()) != nil {
		response.Forbidden(c, "Kiro authorization requires an administrator")
		return 0, false
	}
	return subject.UserID, true
}

func kiroOAuthJSON(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		response.BadRequest(c, "Invalid Kiro authorization request")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, "Invalid Kiro authorization request")
		return false
	}
	return true
}

func (h *KiroOAuthHandler) reply(c *gin.Context, result *service.KiroOAuthResult, err error) {
	c.Header("Cache-Control", "no-store")
	if err != nil {
		if infraerrors.Reason(err) == "KIRO_OAUTH_PERSISTENCE_UNKNOWN" {
			c.Header("Retry-After", "2")
		}
		middleware.SetAuditExtra(c, map[string]any{"error_code": infraerrors.Reason(err)})
		response.ErrorFrom(c, err)
		return
	}
	middleware.SetAuditExtra(c, map[string]any{"result": result.Status, "error_code": result.ErrorCode})
	if result.RetryAfter > 0 {
		c.Header("Retry-After", strconv.FormatInt(result.RetryAfter, 10))
	}
	response.Success(c, result)
}

func (h *KiroOAuthHandler) Start(c *gin.Context) {
	owner, ok := kiroOAuthOwner(c)
	if !ok {
		return
	}
	var input service.KiroOAuthStartInput
	if !kiroOAuthJSON(c, &input) {
		return
	}
	result, err := h.service.Start(c.Request.Context(), owner, input)
	h.reply(c, result, err)
}

func (h *KiroOAuthHandler) Complete(c *gin.Context) {
	owner, ok := kiroOAuthOwner(c)
	if !ok {
		return
	}
	var input struct {
		CallbackURL string `json:"callback_url"`
	}
	if !kiroOAuthJSON(c, &input) {
		return
	}
	result, err := h.service.Complete(c.Request.Context(), owner, c.Param("id"), input.CallbackURL)
	h.reply(c, result, err)
}

func (h *KiroOAuthHandler) action(c *gin.Context, run func(context.Context, int64, string) (*service.KiroOAuthResult, error)) {
	owner, ok := kiroOAuthOwner(c)
	if !ok {
		return
	}
	if c.Request.Body != nil {
		data, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
		if err != nil || len(data) != 0 {
			response.BadRequest(c, "Kiro authorization action takes no request body")
			return
		}
	}
	result, err := run(c.Request.Context(), owner, c.Param("id"))
	h.reply(c, result, err)
}

func (h *KiroOAuthHandler) Poll(c *gin.Context)     { h.action(c, h.service.Poll) }
func (h *KiroOAuthHandler) Finalize(c *gin.Context) { h.action(c, h.service.Finalize) }
func (h *KiroOAuthHandler) Status(c *gin.Context)   { h.action(c, h.service.Status) }
func (h *KiroOAuthHandler) Cancel(c *gin.Context)   { h.action(c, h.service.Cancel) }
