package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
)

func trafficFilter(c *gin.Context) (controltraffic.Filter, bool) {
	from, err := time.Parse(time.RFC3339, c.Query("from"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "from 必须为 RFC3339 时间", nil)
		return controltraffic.Filter{}, false
	}
	to, err := time.Parse(time.RFC3339, c.Query("to"))
	if err != nil {
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "to 必须为 RFC3339 时间", nil)
		return controltraffic.Filter{}, false
	}
	return controltraffic.Filter{From: from, To: to, Interval: c.Query("interval"), UserID: c.Query("userId"), ProtocolID: c.Query("protocolId")}, true
}

func (h *handler) trafficSeries(c *gin.Context) {
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	value, err := h.Traffic.Series(c.Request.Context(), filter)
	if err != nil {
		writeTrafficError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": value})
}

func (h *handler) trafficSummary(c *gin.Context) {
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	value, err := h.Traffic.Summary(c.Request.Context(), filter)
	if err != nil {
		writeTrafficError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) trafficRankings(c *gin.Context) {
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	value, err := h.Traffic.Rankings(c.Request.Context(), filter, c.Query("dimension"), limit)
	if err != nil {
		writeTrafficError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": value})
}

func (h *handler) trafficActiveUsers(c *gin.Context) {
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	value, err := h.Traffic.ActiveUsers(c.Request.Context(), filter)
	if err != nil {
		writeTrafficError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) trafficProtocolShares(c *gin.Context) {
	filter, ok := trafficFilter(c)
	if !ok {
		return
	}
	value, err := h.Traffic.ProtocolShares(c.Request.Context(), filter)
	if err != nil {
		writeTrafficError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": value})
}

func writeTrafficError(c *gin.Context, err error) {
	if errors.Is(err, controltraffic.ErrInvalidRequest) {
		httpx.Error(c, http.StatusBadRequest, httpx.CodeTrafficInvalid, err.Error(), nil)
		return
	}
	writeInternalError(c, "内部服务器错误")
}
