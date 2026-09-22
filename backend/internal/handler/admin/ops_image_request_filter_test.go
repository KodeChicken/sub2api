package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type imageRequestOpsRepo struct {
	service.OpsRepository
	filter *service.OpsErrorLogFilter
}

func (r *imageRequestOpsRepo) ListErrorLogs(_ context.Context, filter *service.OpsErrorLogFilter) (*service.OpsErrorLogList, error) {
	r.filter = filter
	return &service.OpsErrorLogList{Errors: []*service.OpsErrorLog{}, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func TestOpsUpstreamErrorsFiltersImageRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &imageRequestOpsRepo{}
	ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/upstream-errors", NewOpsHandler(ops).ListUpstreamErrors)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/upstream-errors?request_id=image-request-123&time_range=30d", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, repo.filter)
	require.Equal(t, "image-request-123", repo.filter.RequestID)
}
