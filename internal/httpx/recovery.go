package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
)

// RecoveryMiddleware converts handler panics into the standard error envelope.
// The control plane hosts the embedded sing-box core in-process, so an
// unrecovered panic would take the proxy down with the panel.
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("处理请求发生 panic",
					"requestId", RequestID(c),
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
				Error(c, http.StatusInternalServerError, CodeInternalError, "服务器内部错误", nil)
			}
		}()
		c.Next()
	}
}
