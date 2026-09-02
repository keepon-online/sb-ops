package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

type argoTunnelsResponse struct {
	Items []serviceargo.Tunnel `json:"items"`
}

type argoTunnelRequest struct {
	Revision         int64   `json:"revision"`
	Name             string  `json:"name" binding:"required"`
	Enabled          *bool   `json:"enabled" binding:"required"`
	Mode             string  `json:"mode" binding:"required"`
	FixedDomain      string  `json:"fixedDomain"`
	PreferredAddress string  `json:"preferredAddress"`
	ProtocolID       string  `json:"protocolId" binding:"required"`
	Token            *string `json:"token"`
}

func (h *handler) listArgoTunnels(c *gin.Context) {
	values, err := h.Argo.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, argoTunnelsResponse{Items: values})
}

func (h *handler) createArgoTunnel(c *gin.Context) {
	request, ok := bindArgoTunnelRequest(c)
	if !ok {
		return
	}
	value, err := h.Argo.Create(c.Request.Context(), request)
	if !h.mutate(c, err, actionArgoTunnelCreate, resourceArgoTunnel, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) updateArgoTunnel(c *gin.Context) {
	request, ok := bindArgoTunnelRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	value, err := h.Argo.Update(c.Request.Context(), request)
	if !h.mutate(c, err, actionArgoTunnelUpdate, resourceArgoTunnel, c.Param("id"), value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteArgoTunnel(c *gin.Context) {
	err := h.Argo.Delete(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionArgoTunnelDelete, resourceArgoTunnel, c.Param("id"), c.Param("id")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) restartArgoTunnel(c *gin.Context) {
	if h.ArgoManager == nil {
		httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeInternalError, "Argo 隧道管理器不可用", nil)
		return
	}
	values, err := h.Argo.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	for _, value := range values {
		if value.ID == c.Param("id") {
			h.ArgoManager.RestartTunnel(value.ID)
			c.Status(http.StatusNoContent)
			return
		}
	}
	writeResourceError(c, common.ErrNotFound)
}

func bindArgoTunnelRequest(c *gin.Context) (serviceargo.TunnelWriteInput, bool) {
	var request argoTunnelRequest
	if !httpx.BindJSON(c, &request, "Argo 隧道配置无效") {
		return serviceargo.TunnelWriteInput{}, false
	}
	return serviceargo.TunnelWriteInput{
		Revision: request.Revision, Name: request.Name, Enabled: *request.Enabled, Mode: request.Mode,
		FixedDomain: request.FixedDomain, PreferredAddress: request.PreferredAddress,
		ProtocolID: request.ProtocolID, Token: request.Token,
	}, true
}
