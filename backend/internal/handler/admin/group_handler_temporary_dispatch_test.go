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
	startInput       service.StartTemporaryDispatchInput
	startCalls       int
	stopIDs          []int64
	stopCalls        int
	previewAccountID int64
	previewWindow    string
	previewCalls     int
	getGroupID       int64
	getCalls         int
	adjustInput      service.AdjustTemporaryDispatchInput
	adjustCalls      int
}

func (s *temporaryDispatchAdminServiceStub) GetTemporaryDispatch(_ context.Context, groupID int64) (*service.TemporaryDispatchResult, error) {
	s.getCalls++
	s.getGroupID = groupID
	return &service.TemporaryDispatchResult{
		DispatchID: "td_handler_test", GroupIDs: []int64{11, 12}, Mode: service.TemporaryDispatchModeHybrid,
		Accounts: []service.TemporaryDispatchAccountResult{{AccountID: 88, UsageMetric: service.TemporaryDispatchUsageQuotaPercent}},
	}, nil
}

func (s *temporaryDispatchAdminServiceStub) AdjustTemporaryDispatch(_ context.Context, input service.AdjustTemporaryDispatchInput) (*service.TemporaryDispatchResult, error) {
	s.adjustCalls++
	s.adjustInput = input
	return &service.TemporaryDispatchResult{DispatchID: "td_handler_test", GroupIDs: []int64{11, 12}}, nil
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

func (s *temporaryDispatchAdminServiceStub) GetTemporaryDispatchQuotaPreview(_ context.Context, accountID int64, window string) (*service.TemporaryDispatchQuotaPreview, error) {
	s.previewCalls++
	s.previewAccountID = accountID
	s.previewWindow = window
	return &service.TemporaryDispatchQuotaPreview{Window: window, UsedPercent: 35, ResetAt: time.Now().Add(time.Hour)}, nil
}

func setupTemporaryDispatchGroupRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewGroupHandler(svc, nil, nil)
	router.POST("/api/v1/admin/groups/temporary-dispatch", handler.StartTemporaryDispatch)
	router.GET("/api/v1/admin/groups/temporary-dispatch", handler.GetTemporaryDispatch)
	router.PATCH("/api/v1/admin/groups/temporary-dispatch", handler.AdjustTemporaryDispatch)
	router.POST("/api/v1/admin/groups/temporary-dispatch/stop", handler.StopTemporaryDispatch)
	router.GET("/api/v1/admin/groups/temporary-dispatch/quota-preview", handler.GetTemporaryDispatchQuotaPreview)
	return router
}

func TestGroupHandlerStartsTemporaryDispatch(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{"group_ids":[11,12],"account_id":88,"mode":"hybrid","duration_minutes":90,"quota_window":"5h","target_delta_percent":30}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.startCalls)
	require.Equal(t, []int64{11, 12}, svc.startInput.GroupIDs)
	require.Equal(t, int64(88), svc.startInput.AccountID)
	require.Equal(t, service.TemporaryDispatchModeHybrid, svc.startInput.Mode)
	require.Equal(t, 90, svc.startInput.DurationMinutes)
	require.Equal(t, service.TemporaryDispatchQuotaWindow5h, svc.startInput.QuotaWindow)
	require.InDelta(t, 30, svc.startInput.TargetDeltaPercent, 0.001)
	require.Contains(t, recorder.Body.String(), `"dispatch_id":"td_handler_test"`)
}

func TestGroupHandlerStartsTemporaryDispatchWithAccountCost(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{"group_ids":[11],"account_id":88,"mode":"usage","quota_window":"5h","target_cost":200}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.InDelta(t, 200, svc.startInput.TargetCost, 0.001)
}

func TestGroupHandlerGetsAndAdjustsSharedTemporaryDispatch(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)

	getRecorder := httptest.NewRecorder()
	router.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/temporary-dispatch?group_id=11", nil))
	require.Equal(t, http.StatusOK, getRecorder.Code)
	require.Equal(t, int64(11), svc.getGroupID)
	require.Contains(t, getRecorder.Body.String(), `"dispatch_id":"td_handler_test"`)

	patchRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{"group_id":11,"accounts":[{"account_id":88,"additional_usage":10,"extend_duration_minutes":30}]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(patchRecorder, request)

	require.Equal(t, http.StatusOK, patchRecorder.Code)
	require.Equal(t, 1, svc.adjustCalls)
	require.Equal(t, int64(11), svc.adjustInput.GroupID)
	require.Equal(t, service.TemporaryDispatchAdjustment{AccountID: 88, AdditionalUsage: 10, ExtendDurationMins: 30}, svc.adjustInput.Accounts[0])
}

func TestGroupHandlerStartsTemporaryDispatchAccountPool(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/temporary-dispatch", strings.NewReader(`{
		"group_ids":[11,12],
		"mode":"hybrid",
		"accounts":[
			{"account_id":88,"duration_minutes":30,"quota_window":"5h","target_delta_percent":10},
			{"account_id":89,"duration_minutes":90,"quota_window":"7d","target_delta_percent":25}
		]
	}`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.startCalls)
	require.Len(t, svc.startInput.Accounts, 2)
	require.Equal(t, int64(88), svc.startInput.Accounts[0].AccountID)
	require.Equal(t, 30, svc.startInput.Accounts[0].DurationMinutes)
	require.Equal(t, service.TemporaryDispatchQuotaWindow5h, svc.startInput.Accounts[0].QuotaWindow)
	require.InDelta(t, 10, svc.startInput.Accounts[0].TargetDeltaPercent, 0.001)
	require.Equal(t, int64(89), svc.startInput.Accounts[1].AccountID)
	require.Equal(t, 90, svc.startInput.Accounts[1].DurationMinutes)
	require.Equal(t, service.TemporaryDispatchQuotaWindow7d, svc.startInput.Accounts[1].QuotaWindow)
	require.InDelta(t, 25, svc.startInput.Accounts[1].TargetDeltaPercent, 0.001)
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

func TestGroupHandlerGetsTemporaryDispatchQuotaPreview(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/temporary-dispatch/quota-preview?account_id=88&quota_window=7d", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, svc.previewCalls)
	require.Equal(t, int64(88), svc.previewAccountID)
	require.Equal(t, service.TemporaryDispatchQuotaWindow7d, svc.previewWindow)
	require.Contains(t, recorder.Body.String(), `"used_percent":35`)
}

func TestGroupHandlerQuotaPreviewDefaultsTo5h(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/temporary-dispatch/quota-preview?account_id=88", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, service.TemporaryDispatchQuotaWindow5h, svc.previewWindow)
}

func TestGroupHandlerRejectsInvalidQuotaPreviewAccount(t *testing.T) {
	svc := &temporaryDispatchAdminServiceStub{}
	router := setupTemporaryDispatchGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/temporary-dispatch/quota-preview?account_id=invalid", nil)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, svc.previewCalls)
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
