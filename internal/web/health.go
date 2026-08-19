package web

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/observability"
)

func RegisterHealthRoutes(router *gin.Engine, db *sql.DB, health *observability.HealthState, expectedSchema int) {
	router.GET("/health/live", func(c *gin.Context) {
		httpx.JSON(c, http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/health/ready", func(c *gin.Context) {
		readiness := health.Snapshot(c.Request.Context(), db, expectedSchema)
		if readiness.Status != "ready" {
			// 未就绪时的组件明细属于运营侦察信息，不向未认证方暴露；
			// 就绪状态本身（status）足以驱动负载均衡器摘流。
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"code": "NOT_READY", "message": "control plane is not ready", "requestId": httpx.RequestID(c), "status": readiness.Status,
			})
			return
		}
		httpx.JSON(c, http.StatusOK, readiness)
	})
}
