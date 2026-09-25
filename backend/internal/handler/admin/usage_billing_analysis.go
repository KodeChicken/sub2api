package admin

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// billingAnalysisFilters keeps the model and user breakdowns on the same scope.
func billingAnalysisFilters(c *gin.Context) (usagestats.UsageLogFilters, bool) {
	var filters usagestats.UsageLogFilters
	for _, item := range []struct {
		key string
		out *int64
	}{
		{"user_id", &filters.UserID},
		{"api_key_id", &filters.APIKeyID},
		{"account_id", &filters.AccountID},
		{"group_id", &filters.GroupID},
	} {
		if raw := c.Query(item.key); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				response.BadRequest(c, "Invalid "+item.key)
				return filters, false
			}
			*item.out = id
		}
	}
	filters.RequestID = strings.TrimSpace(c.Query("request_id"))
	filters.Model = c.Query("model")
	filters.ModelFilterSource = usagestats.ModelSourceRequested
	filters.BillingMode = strings.TrimSpace(c.Query("billing_mode"))
	if raw := strings.TrimSpace(c.Query("request_type")); raw != "" {
		value, err := service.ParseUsageRequestType(raw)
		if err != nil {
			response.BadRequest(c, err.Error())
			return filters, false
		}
		requestType := int16(value)
		filters.RequestType = &requestType
	} else if raw := c.Query("stream"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid stream value")
			return filters, false
		}
		filters.Stream = &value
	}
	var err error
	filters.NativeCompactionV2, err = parseOptionalBoolDashboardFilter(c, "native_compaction_v2")
	if err != nil {
		response.BadRequest(c, "Invalid native_compaction_v2 value")
		return filters, false
	}
	filters.UpstreamModelMismatch, err = parseOptionalBoolDashboardFilter(c, "upstream_model_mismatch")
	if err != nil {
		response.BadRequest(c, "Invalid upstream_model_mismatch value")
		return filters, false
	}
	if raw := c.Query("billing_type"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return filters, false
		}
		billingType := int8(value)
		filters.BillingType = &billingType
	}

	const location = "Asia/Shanghai"
	now := timezone.NowInUserLocation(location)
	startDate, endDate := c.Query("start_date"), c.Query("end_date")
	if (startDate == "") != (endDate == "") {
		response.BadRequest(c, "start_date and end_date must be provided together")
		return filters, false
	}
	var start, end time.Time
	if startDate != "" {
		start, err = timezone.ParseInUserLocation("2006-01-02", startDate, location)
		if err != nil {
			response.BadRequest(c, "Invalid start_date")
			return filters, false
		}
		end, err = timezone.ParseInUserLocation("2006-01-02", endDate, location)
		if err != nil || end.Before(start) {
			response.BadRequest(c, "Invalid end_date")
			return filters, false
		}
		end = end.AddDate(0, 0, 1)
	} else {
		start = timezone.StartOfDayInUserLocation(now, location)
		end = now
	}
	if end.After(now) {
		end = now
	}
	filters.StartTime, filters.EndTime = &start, &end
	return filters, true
}

// BillingAnalysis returns the persisted U/A totals and model breakdown.
func (h *UsageHandler) BillingAnalysis(c *gin.Context) {
	filters, ok := billingAnalysisFilters(c)
	if !ok {
		return
	}
	analysis, err := h.usageService.GetBillingAnalysis(c.Request.Context(), filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, analysis)
}

func (h *UsageHandler) BillingAnalysisUsers(c *gin.Context) {
	if _, present := c.GetQuery("model"); !present {
		response.BadRequest(c, "model is required")
		return
	}
	filters, ok := billingAnalysisFilters(c)
	if !ok {
		return
	}
	page, pageSize := 1, 50
	if raw := c.Query("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 10000 {
			response.BadRequest(c, "Invalid page")
			return
		}
		page = value
	}
	if raw := c.Query("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			response.BadRequest(c, "Invalid page_size")
			return
		}
		pageSize = value
	}
	users, err := h.usageService.GetBillingAnalysisUsers(c.Request.Context(), filters, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, users)
}
