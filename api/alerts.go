package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

func (h *handler) listAlerts(c *gin.Context) {
	values, err := h.Alerts.List(c.Request.Context(), c.Query("status"), c.Query("severity"), c.Query("type"))
	if err != nil {
		writeInternalError(c, "查询告警失败")
		return
	}
	stats, err := h.Alerts.NotificationStats(c.Request.Context())
	if err != nil {
		writeInternalError(c, "读取通知状态失败")
		return
	}
	httpx.OK(c, gin.H{"items": values, "notifications": stats})
}

func (h *handler) alertStats(c *gin.Context) {
	days := 30
	if raw := c.Query("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 90 {
			httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "days 需为 1-90 的整数", nil)
			return
		}
		days = parsed
	}
	stats, err := h.Alerts.Stats(c.Request.Context(), days)
	if err != nil {
		writeInternalError(c, "查询告警统计失败")
		return
	}
	httpx.OK(c, stats)
}

func (h *handler) acknowledgeAlert(c *gin.Context) {
	value, err := h.Alerts.Acknowledge(c.Request.Context(), c.Param("id"), currentPrincipal(c).AccountID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "告警不存在", nil)
		} else {
			writeInternalError(c, "确认告警失败")
		}
		return
	}
	h.audit(c, actionAlertAcknowledge, resourceAlert, value.ID, ResultSuccess)
	httpx.OK(c, value)
}
