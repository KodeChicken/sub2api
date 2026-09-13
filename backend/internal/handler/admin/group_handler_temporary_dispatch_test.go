//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type temporaryDispatchAdminServiceStub struct {
	service.AdminService
	startInput service.StartTemporaryDispatchInput
	startCalls int
	stopIDs    []int64
	stopCalls  int
}

func (s *temporaryDispatchAdminServiceStub) StartTemporaryDispatch(_ context.Context, input service.StartTemporaryDispatchInput) (*service.TemporaryDispatchResult, error) {
	s.startCalls++
	s.startInput = input
	startedAt := time.Date(2026, time.September, 12, 10, 30, 0, 0, time.UTC)
	return &service.TemporaryDispatchResult{
		DispatchID: "td_handler_test",
		GroupIDs:   input.GroupIDs,
		AccountID:  input.AccountID,
		StartedAt:  startedAt,
		ExpiresAt:  startedAt.Add(time.Duration(input.DurationMinutes) * time.Minute),
	}, nil
}

func (s *temporaryDispatchAdminServiceStub) StopTemporaryDispatch(_ context.Context, groupIDs []int64) error {
	s.stopCalls++
	s.stopIDs = append([]int64(nil), groupIDs...)
	return nil
}

func setupTemporaryDispatchGroupRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewGroupHandler(svc, nil, nil)
	router.POST("/api/v1/admin/groups/temporary-dispatch", handler.StartTemporaryDispatch)
	router.POST("/api/v1/admin/groups/temporary-dispatch/stop", handler.StopTemporaryDispatch)
	return router
}

func TestGroupHandlerStartsTemporaryDispatch(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{"group_ids":[11,12],"account_id":88,"duration_minutes":90}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.startCalls)
	require.Equal(t, []int64{11, 12}, svc.startInput.GroupIDs)
	require.Equal(t, int64(88), svc.startInput.AccountID)
	require.Equal(t, 90, svc.startInput.DurationMinutes)
	require.Contains(t, recorder.Body.String(), `"dispatch_id":"td_handler_test"`)
}

func TestGroupHandlerStopsTemporaryDispatch(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch/stop", strings.NewReader(`{"group_ids":[11,12]}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.stopCalls)
	require.Equal(t, []int64{11, 12}, svc.stopIDs)
}

func TestGroupHandlerRejectsInvalidTemporaryDispatchPayload(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{"group_ids":[],"account_id":"invalid"}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, svc.startCalls)
}
