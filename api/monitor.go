package api

import (
	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
)

// getMonitorStatus 返回最近一次采样的服务器指标快照与趋势历史；只读、
// 不会失败，无需新增业务错误码。
func (h *handler) getMonitorStatus(c *gin.Context) {
	httpx.OK(c, h.Monitor.Snapshot())
}
