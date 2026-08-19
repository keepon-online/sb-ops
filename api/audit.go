package api

import (
	"github.com/gin-gonic/gin"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"strconv"
)

func (h *handler) listAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	value, err := h.auditLogs.List(c.Request.Context(), controlaudit.Query{Cursor: c.Query("cursor"), Limit: limit, Actor: c.Query("actor"), Resource: c.Query("resource"), Action: c.Query("action"), Result: c.Query("result"), From: c.Query("from"), To: c.Query("to")})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}
