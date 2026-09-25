package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageCostEstimateRepo struct {
	service.SettingRepository
	value string
}

func (r *usageCostEstimateRepo) GetValue(context.Context, string) (string, error) {
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}

func (r *usageCostEstimateRepo) Set(_ context.Context, _, value string) error {
	r.value = value
	return nil
}

func (r *usageCostEstimateRepo) Delete(context.Context, string) error {
	r.value = ""
	return nil
}

func TestUsageCostEstimateAdminHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &usageCostEstimateRepo{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, &config.Config{})}
	router := gin.New()
	router.GET("/settings/usage-cost-estimate", h.GetUsageCostEstimate)
	router.PUT("/settings/usage-cost-estimate", h.UpdateUsageCostEstimate)

	request := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, "/settings/usage-cost-estimate", bytes.NewBufferString(body)))
		return rec
	}
	require.Contains(t, request(http.MethodGet, "").Body.String(), `"weekly_cost_usd":0`)
	for _, body := range []string{
		`{}`, `{"weekly_cost_usd":17}`, `{"weekly_cost_usd":17,"weekly_quota_usd":0}`,
		`{"weekly_cost_usd":-1,"weekly_quota_usd":100}`,
	} {
		require.Equal(t, http.StatusBadRequest, request(http.MethodPut, body).Code, body)
	}
	rec := request(http.MethodPut, `{"weekly_cost_usd":17,"weekly_quota_usd":100}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, request(http.MethodGet, "").Body.String(), `"weekly_quota_usd":100`)
	require.Equal(t, http.StatusOK, request(http.MethodPut, `{"weekly_cost_usd":0,"weekly_quota_usd":0}`).Code)
	require.Empty(t, repo.value)
}
