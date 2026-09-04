package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

type singBoxConfigRequest struct {
	Revision           int64                      `json:"revision"`
	LogLevel           string                     `json:"logLevel" binding:"required"`
	LogTimestamp       *bool                      `json:"logTimestamp" binding:"required"`
	DNSServers         []servicesingbox.DNSServer `json:"dnsServers"`
	DNSFinal           string                     `json:"dnsFinal"`
	ClashAPIEnabled    *bool                      `json:"clashApiEnabled" binding:"required"`
	ClashAPIListen     string                     `json:"clashApiListen"`
	ClashAPISecret     *string                    `json:"clashApiSecret"`
	V2RayAPIEnabled    *bool                      `json:"v2rayApiEnabled" binding:"required"`
	V2RayAPIListen     string                     `json:"v2rayApiListen"`
	DefaultOutboundTag string                     `json:"defaultOutboundTag" binding:"required"`
}

type singBoxOutboundRequest struct {
	Revision   int64                        `json:"revision"`
	Type       string                       `json:"type"`
	Tag        string                       `json:"tag" binding:"required"`
	Server     string                       `json:"server"`
	ServerPort int                          `json:"serverPort"`
	Username   string                       `json:"username"`
	Password   *string                      `json:"password"`
	Warp       *servicesingbox.WarpSettings `json:"warp"`
}

type singBoxRouteRuleRequest struct {
	Revision    int64                          `json:"revision"`
	Name        string                         `json:"name" binding:"required"`
	Enabled     *bool                          `json:"enabled" binding:"required"`
	Priority    int                            `json:"priority"`
	Conditions  servicesingbox.RouteConditions `json:"conditions"`
	OutboundTag string                         `json:"outboundTag" binding:"required"`
}

type singBoxRuleSetRequest struct {
	Revision       int64  `json:"revision"`
	Tag            string `json:"tag" binding:"required"`
	Format         string `json:"format" binding:"required"`
	URL            string `json:"url" binding:"required"`
	DownloadDetour string `json:"downloadDetour"`
}

// writeRuntimeUnavailable 收敛「sing-box 运行时未装配」的 503 响应。
func writeRuntimeUnavailable(c *gin.Context) {
	httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable, "sing-box 运行时尚未配置", nil)
}

// writeRuntimeFailed 收敛运行时操作失败（连接查询/关闭、重启等）的 500 响应；
// 配置被内核拒绝的 422（dry-run 校验）语义不同，仍走 httpx.Error。
func writeRuntimeFailed(c *gin.Context, message string) {
	httpx.Error(c, http.StatusInternalServerError, httpx.CodeRuntimeFailed, message, nil)
}

func (h *handler) listSingBoxConnections(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	value, err := h.Sync.Connections(c.Request.Context())
	if err != nil {
		slog.Error("获取实时连接失败", "error", err)
		writeRuntimeFailed(c, "获取实时连接失败")
		return
	}
	httpx.OK(c, value)
}

func (h *handler) closeSingBoxConnection(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	if err := h.Sync.CloseConnection(c.Request.Context(), c.Param("id")); err != nil {
		h.audit(c, actionSingBoxConnectionClose, resourceSingBoxConnection, c.Param("id"), resultFailure)
		slog.Error("关闭连接失败", "error", err, "connectionId", c.Param("id"))
		writeRuntimeFailed(c, "关闭连接失败")
		return
	}
	h.audit(c, actionSingBoxConnectionClose, resourceSingBoxConnection, c.Param("id"), ResultSuccess)
	c.Status(http.StatusNoContent)
}

func (h *handler) getSingBoxRuntime(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	httpx.OK(c, h.Sync.RuntimeStatus(c.Request.Context()))
}

func (h *handler) checkSingBoxRuntime(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	value, err := h.Sync.Check(c.Request.Context())
	if err != nil {
		h.audit(c, actionSingBoxRuntimeCheck, resourceSingBoxRuntime, localRuntimeID, resultFailure)
		httpx.Error(c, http.StatusUnprocessableEntity, httpx.CodeRuntimeFailed, value.Message, nil)
		return
	}
	h.audit(c, actionSingBoxRuntimeCheck, resourceSingBoxRuntime, localRuntimeID, ResultSuccess)
	httpx.OK(c, value)
}

func (h *handler) reconcileSingBoxRuntime(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	value, err := h.Sync.Reconcile(c.Request.Context())
	if err != nil {
		h.audit(c, actionSingBoxRuntimeReconcile, resourceSingBoxRuntime, localRuntimeID, resultFailure)
		slog.Error("同步 sing-box 配置失败", "error", err)
		httpx.Error(c, http.StatusInternalServerError, httpx.CodeSyncFailed, "同步 sing-box 配置失败", nil)
		return
	}
	h.audit(c, actionSingBoxRuntimeReconcile, resourceSingBoxRuntime, localRuntimeID, ResultSuccess)
	httpx.OK(c, value)
}

func (h *handler) restartSingBoxRuntime(c *gin.Context) {
	if h.Sync == nil {
		writeRuntimeUnavailable(c)
		return
	}
	if err := h.Sync.Restart(c.Request.Context()); err != nil {
		h.audit(c, actionSingBoxRuntimeRestart, resourceSingBoxRuntime, localRuntimeID, resultFailure)
		slog.Error("重启 sing-box 失败", "error", err)
		writeRuntimeFailed(c, "重启 sing-box 失败")
		return
	}
	h.audit(c, actionSingBoxRuntimeRestart, resourceSingBoxRuntime, localRuntimeID, ResultSuccess)
	c.Status(http.StatusNoContent)
}

func (h *handler) getSingBoxConfig(c *gin.Context) {
	value, err := h.SingBox.GetConfig(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) updateSingBoxConfig(c *gin.Context) {
	request, ok := bindSingBoxConfigRequest(c)
	if !ok {
		return
	}
	value, err := h.SingBox.UpdateConfig(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxConfigUpdate, resourceSingBoxConfig, singBoxConfigID, singBoxConfigID) {
		return
	}
	httpx.OK(c, value)
}

// applySingBoxAITemplate creates or updates the one-click「AI 解锁」route
// rule (OpenAI/Gemini/Anthropic → selected outbound).
func (h *handler) applySingBoxAITemplate(c *gin.Context) {
	var request struct {
		OutboundTag string `json:"outboundTag" binding:"required"`
	}
	if !httpx.BindJSON(c, &request, "AI 解锁模板无效") {
		return
	}
	value, err := h.SingBox.ApplyAIUnlockTemplate(c.Request.Context(), request.OutboundTag)
	if !h.mutate(c, err, actionSingBoxRouteRuleCreate, resourceSingBoxRouteRule, "", value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) listSingBoxOutbounds(c *gin.Context) {
	values, err := h.SingBox.ListOutbounds(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicesingbox.Outbound]{Items: values})
}

func (h *handler) createSingBoxOutbound(c *gin.Context) {
	request, ok := bindSingBoxOutboundRequest(c)
	if !ok {
		return
	}
	value, err := h.SingBox.CreateOutbound(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxOutboundCreate, resourceSingBoxOutbound, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

// registerSingBoxWarp provisions a fresh free WARP account and returns the
// ready-to-save outbound settings; persistence happens through the normal
// outbound write path, so this endpoint is side-effect free.
func (h *handler) registerSingBoxWarp(c *gin.Context) {
	settings, err := h.SingBox.RegisterWarp(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, settings)
}

func (h *handler) updateSingBoxOutbound(c *gin.Context) {
	request, ok := bindSingBoxOutboundRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	before, beforeErr := h.SingBox.GetOutbound(c.Request.Context(), request.ID)
	value, err := h.SingBox.UpdateOutbound(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxOutboundUpdate, resourceSingBoxOutbound, request.ID, value.ID, diffable(beforeErr, err, before, value)) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteSingBoxOutbound(c *gin.Context) {
	id := c.Param("id")
	err := h.SingBox.DeleteOutbound(c.Request.Context(), id)
	if !h.mutate(c, err, actionSingBoxOutboundDelete, resourceSingBoxOutbound, id, id) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) listSingBoxRouteRules(c *gin.Context) {
	values, err := h.SingBox.ListRules(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicesingbox.RouteRule]{Items: values})
}

func (h *handler) createSingBoxRouteRule(c *gin.Context) {
	request, ok := bindSingBoxRouteRuleRequest(c)
	if !ok {
		return
	}
	value, err := h.SingBox.CreateRule(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxRouteRuleCreate, resourceSingBoxRouteRule, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) updateSingBoxRouteRule(c *gin.Context) {
	request, ok := bindSingBoxRouteRuleRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	before, beforeErr := h.SingBox.GetRule(c.Request.Context(), request.ID)
	value, err := h.SingBox.UpdateRule(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxRouteRuleUpdate, resourceSingBoxRouteRule, request.ID, value.ID, diffable(beforeErr, err, before, value)) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteSingBoxRouteRule(c *gin.Context) {
	id := c.Param("id")
	err := h.SingBox.DeleteRule(c.Request.Context(), id)
	if !h.mutate(c, err, actionSingBoxRouteRuleDelete, resourceSingBoxRouteRule, id, id) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) orderSingBoxRouteRules(c *gin.Context) {
	var request servicesingbox.OrderInput
	if !httpx.BindJSON(c, &request, "请选择要处理的数据") {
		return
	}
	values, err := h.SingBox.OrderRules(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxRouteRuleOrder, resourceSingBoxConfig, singBoxConfigID, singBoxConfigID) {
		return
	}
	httpx.OK(c, itemsResponse[servicesingbox.RouteRule]{Items: values})
}

func (h *handler) dryRunSingBoxRoutes(c *gin.Context) {
	var request servicesingbox.DryRunInput
	if !httpx.BindJSON(c, &request, "测试输入无效") {
		return
	}
	value, err := h.SingBox.DryRun(c.Request.Context(), request)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) listSingBoxRuleSets(c *gin.Context) {
	values, err := h.SingBox.ListRuleSets(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicesingbox.RuleSet]{Items: values})
}

func (h *handler) createSingBoxRuleSet(c *gin.Context) {
	request, ok := bindSingBoxRuleSetRequest(c)
	if !ok {
		return
	}
	value, err := h.SingBox.CreateRuleSet(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxRuleSetCreate, resourceSingBoxRuleSet, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) updateSingBoxRuleSet(c *gin.Context) {
	request, ok := bindSingBoxRuleSetRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	value, err := h.SingBox.UpdateRuleSet(c.Request.Context(), request)
	if !h.mutate(c, err, actionSingBoxRuleSetUpdate, resourceSingBoxRuleSet, request.ID, value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteSingBoxRuleSet(c *gin.Context) {
	id := c.Param("id")
	err := h.SingBox.DeleteRuleSet(c.Request.Context(), id)
	if !h.mutate(c, err, actionSingBoxRuleSetDelete, resourceSingBoxRuleSet, id, id) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) validateSingBox(c *gin.Context) {
	value, err := h.SingBox.Validate(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func bindSingBoxConfigRequest(c *gin.Context) (servicesingbox.ConfigWriteInput, bool) {
	var request singBoxConfigRequest
	if !httpx.BindJSON(c, &request, "sing-box 配置无效") {
		return servicesingbox.ConfigWriteInput{}, false
	}
	return servicesingbox.ConfigWriteInput{Revision: request.Revision, LogLevel: request.LogLevel, LogTimestamp: *request.LogTimestamp, DNSServers: request.DNSServers, DNSFinal: request.DNSFinal, ClashAPIEnabled: *request.ClashAPIEnabled, ClashAPIListen: request.ClashAPIListen, ClashAPISecret: request.ClashAPISecret, V2RayAPIEnabled: *request.V2RayAPIEnabled, V2RayAPIListen: request.V2RayAPIListen, DefaultOutboundTag: request.DefaultOutboundTag}, true
}

func bindSingBoxOutboundRequest(c *gin.Context) (servicesingbox.OutboundWriteInput, bool) {
	var request singBoxOutboundRequest
	if !httpx.BindJSON(c, &request, "出站配置无效") {
		return servicesingbox.OutboundWriteInput{}, false
	}
	return servicesingbox.OutboundWriteInput{Revision: request.Revision, Type: request.Type, Tag: request.Tag, Server: request.Server, ServerPort: request.ServerPort, Username: request.Username, Password: request.Password, Warp: request.Warp}, true
}

func bindSingBoxRouteRuleRequest(c *gin.Context) (servicesingbox.RouteRuleWriteInput, bool) {
	var request singBoxRouteRuleRequest
	if !httpx.BindJSON(c, &request, "路由规则无效") {
		return servicesingbox.RouteRuleWriteInput{}, false
	}
	return servicesingbox.RouteRuleWriteInput{Revision: request.Revision, Name: request.Name, Enabled: *request.Enabled, Priority: request.Priority, Conditions: request.Conditions, OutboundTag: request.OutboundTag}, true
}

func bindSingBoxRuleSetRequest(c *gin.Context) (servicesingbox.RuleSetWriteInput, bool) {
	var request singBoxRuleSetRequest
	if !httpx.BindJSON(c, &request, "规则集配置无效") {
		return servicesingbox.RuleSetWriteInput{}, false
	}
	return servicesingbox.RuleSetWriteInput{Revision: request.Revision, Tag: request.Tag, Format: request.Format, URL: request.URL, DownloadDetour: request.DownloadDetour}, true
}
