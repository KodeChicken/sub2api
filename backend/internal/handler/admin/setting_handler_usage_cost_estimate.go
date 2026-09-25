package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetUsageCostEstimate(c *gin.Context) {
	value, err := h.settingService.GetUsageCostEstimate(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}

func (h *SettingHandler) UpdateUsageCostEstimate(c *gin.Context) {
	var req struct {
		WeeklyCostUSD  *float64 `json:"weekly_cost_usd" binding:"required"`
		WeeklyQuotaUSD *float64 `json:"weekly_quota_usd" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	value := service.UsageCostEstimate{
		WeeklyCostUSD:  *req.WeeklyCostUSD,
		WeeklyQuotaUSD: *req.WeeklyQuotaUSD,
	}
	if err := h.settingService.SetUsageCostEstimate(c.Request.Context(), value); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}
