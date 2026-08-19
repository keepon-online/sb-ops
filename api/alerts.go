package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

func (h *handler) listAlerts(c *gin.Context) {
	values, err := h.alerts.List(c.Request.Context(), c.Query("status"), c.Query("severity"), c.Query("type"))
	if err != nil {
		writeInternalError(c, "查询告警失败")
		return
	}
	stats, err := h.alerts.NotificationStats(c.Request.Context())
	if err != nil {
		writeInternalError(c, "读取通知状态失败")
		return
	}
	httpx.OK(c, gin.H{"items": values, "notifications": stats})
}

func (h *handler) acknowledgeAlert(c *gin.Context) {
	value, err := h.alerts.Acknowledge(c.Request.Context(), c.Param("id"), currentPrincipal(c).AccountID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "告警不存在", nil)
		} else {
			writeInternalError(c, "确认告警失败")
		}
		return
	}
	h.audit(c, actionAlertAcknowledge, resourceAlert, value.ID, resultSuccess)
	httpx.OK(c, value)
}
