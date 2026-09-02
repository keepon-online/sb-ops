package api

import (
	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
)

func (h *handler) getOverview(c *gin.Context) {
	value, err := h.Overview.Get(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}
