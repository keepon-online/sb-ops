package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/probe"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
)

type protocolPortSuggestionRequest struct {
	Type serviceprotocol.Type `json:"type" binding:"required"`
}

type protocolWriteRequest struct {
	Revision      int64                `json:"revision"`
	Name          string               `json:"name" binding:"required"`
	Type          serviceprotocol.Type `json:"type" binding:"required"`
	Enabled       *bool                `json:"enabled" binding:"required"`
	Listen        string               `json:"listen"`
	ListenPort    int                  `json:"listenPort" binding:"required"`
	Settings      map[string]any       `json:"settings" binding:"required"`
	CertificateID *string              `json:"certificateId"`
}

func (h *handler) suggestProtocolPort(c *gin.Context) {
	var request protocolPortSuggestionRequest
	if !httpx.BindJSON(c, &request, "请选择协议类型") {
		return
	}
	value, err := h.Protocols.SuggestPort(c.Request.Context(), request.Type)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) protocolSchema(c *gin.Context) {
	value, err := h.Protocols.Schema(serviceprotocol.Type(c.Param("type")))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) listProtocols(c *gin.Context) {
	values, err := h.Protocols.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[serviceprotocol.Protocol]{Items: values})
}

func (h *handler) protocolRuntimeStatus(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	value, err := h.Sync.ProtocolRuntimeStatus(c.Request.Context())
	if err != nil {
		slog.Error("获取协议运行状态失败", "error", err)
		writeRuntimeFailed(c, "获取协议运行状态失败")
		return
	}
	httpx.OK(c, value)
}

// protocolProbeResults 返回最近一轮落库的拨测结果（含周期轮与手动触发）。
func (h *handler) protocolProbeResults(c *gin.Context) {
	if h.Prober == nil {
		writeRuntimeUnavailable(c)
		return
	}
	values, err := h.Prober.StoredResults(c.Request.Context())
	if err != nil {
		slog.Error("读取协议拨测结果失败", "error", err)
		writeRuntimeFailed(c, "读取协议拨测结果失败")
		return
	}
	httpx.OK(c, probe.StoredResult{Items: values})
}

// runProtocolProbe 立即拨测（可选 protocolId 只测单协议），同步返回本轮
// 结果并落库。拨测是诊断动作而非资源写：不走 mutate/审计。
func (h *handler) runProtocolProbe(c *gin.Context) {
	if h.Prober == nil {
		writeRuntimeUnavailable(c)
		return
	}
	result, err := h.Prober.Probe(c.Request.Context(), c.Query("protocolId"))
	if err != nil {
		if errors.Is(err, probe.ErrKernelNotReady) {
			httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable, err.Error(), nil)
			return
		}
		slog.Error("协议拨测失败", "error", err)
		writeRuntimeFailed(c, "协议拨测失败")
		return
	}
	httpx.OK(c, result)
}

func (h *handler) createProtocol(c *gin.Context) {
	request, ok := bindProtocolRequest(c)
	if !ok {
		return
	}
	value, err := h.Protocols.Create(c.Request.Context(), request)
	if !h.mutate(c, err, actionProtocolCreate, resourceProtocol, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) getProtocol(c *gin.Context) {
	value, err := h.Protocols.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) updateProtocol(c *gin.Context) {
	request, ok := bindProtocolRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	value, err := h.Protocols.Update(c.Request.Context(), request)
	if !h.mutate(c, err, actionProtocolUpdate, resourceProtocol, request.ID, value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteProtocol(c *gin.Context) {
	err := h.Protocols.Delete(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionProtocolDelete, resourceProtocol, c.Param("id"), c.Param("id")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) cloneProtocol(c *gin.Context) {
	var request serviceprotocol.CloneInput
	if !httpx.BindJSON(c, &request, "请输入名称与监听端口") {
		return
	}
	value, err := h.Protocols.Clone(c.Request.Context(), c.Param("id"), request)
	if !h.mutate(c, err, actionProtocolClone, resourceProtocol, c.Param("id"), value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) rotateProtocolSecret(c *gin.Context) {
	value, err := h.Protocols.RotateSecret(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionProtocolSecretRotate, resourceProtocol, c.Param("id"), c.Param("id")) {
		return
	}
	httpx.OK(c, value)
}

func bindProtocolRequest(c *gin.Context) (serviceprotocol.WriteInput, bool) {
	var request protocolWriteRequest
	if !httpx.BindJSON(c, &request, "协议信息无效") {
		return serviceprotocol.WriteInput{}, false
	}
	return serviceprotocol.WriteInput{
		Revision: request.Revision, Name: request.Name, Type: request.Type, Enabled: *request.Enabled,
		Listen: request.Listen, ListenPort: request.ListenPort, Settings: request.Settings, CertificateID: request.CertificateID,
	}, true
}
