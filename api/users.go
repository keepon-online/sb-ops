package api

import (
	"context"

	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/service/common"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

type userWriteRequest struct {
	Revision          int64      `json:"revision"`
	Name              string     `json:"name" binding:"required"`
	Enabled           *bool      `json:"enabled" binding:"required"`
	QuotaBytes        int64      `json:"quotaBytes"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	ResetIntervalDays int        `json:"resetIntervalDays"`
	ProtocolIDs       []string   `json:"protocolIds"`
	PolicyIDs         []string   `json:"policyIds"`
}

// userMutationResponse 建号只回用户级订阅 token；设备凭据由设备接口返回。
type userMutationResponse struct {
	User                serviceuser.User `json:"user"`
	Subscription        *string          `json:"subscriptionToken,omitempty"`
	SubscriptionBaseURL string           `json:"subscriptionBaseUrl,omitempty"`
}

type batchStatusRequest struct {
	IDs     []string `json:"ids" binding:"required"`
	Enabled *bool    `json:"enabled" binding:"required"`
}

// batchQuotaRequest 复用单用户额度调整的口径：mode add|set + bytes（set 0 = 无限额）。
type batchQuotaRequest struct {
	IDs   []string `json:"ids" binding:"required"`
	Mode  string   `json:"mode" binding:"required"`
	Bytes int64    `json:"bytes"`
}

type batchProtocolsRequest struct {
	IDs         []string `json:"ids" binding:"required"`
	Mode        string   `json:"mode" binding:"required"`
	ProtocolIDs []string `json:"protocolIds"`
}

func (h *handler) listUsers(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.Users.List(c.Request.Context(), serviceuser.ListInput{
		Cursor: c.Query("cursor"), Limit: limit, Query: c.Query("query"),
		Status: serviceuser.Status(c.Query("status")), Sort: c.Query("sort"),
	})
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, page)
}

func (h *handler) createUser(c *gin.Context) {
	var request userWriteRequest
	if !httpx.BindJSON(c, &request, "用户信息无效") {
		return
	}
	value, token, err := h.Users.Create(c.Request.Context(), serviceuser.CreateInput{
		Name: request.Name, Enabled: *request.Enabled, QuotaBytes: request.QuotaBytes,
		ExpiresAt: request.ExpiresAt, ResetIntervalDays: request.ResetIntervalDays,
		ProtocolIDs: request.ProtocolIDs, PolicyIDs: request.PolicyIDs,
	})
	if !h.mutate(c, err, actionUserCreate, ResourceUser, "", value.ID) {
		return
	}
	httpx.Created(c, userMutationResponse{
		User:                value,
		Subscription:        &token,
		SubscriptionBaseURL: h.subscriptionBaseURL(c),
	})
}

func (h *handler) getUser(c *gin.Context) {
	value, err := h.Users.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) updateUser(c *gin.Context) {
	var request userWriteRequest
	if !httpx.BindJSON(c, &request, "用户信息无效") {
		return
	}
	value, err := h.Users.Update(c.Request.Context(), serviceuser.UpdateInput{
		ID: c.Param("id"), Revision: request.Revision, Name: request.Name, Enabled: *request.Enabled,
		QuotaBytes: request.QuotaBytes, ExpiresAt: request.ExpiresAt,
		ResetIntervalDays: request.ResetIntervalDays, ProtocolIDs: request.ProtocolIDs, PolicyIDs: request.PolicyIDs,
	})
	if !h.mutate(c, err, actionUserUpdate, ResourceUser, c.Param("id"), value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteUser(c *gin.Context) {
	err := h.Users.Delete(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionUserDelete, ResourceUser, c.Param("id"), c.Param("id")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) batchUserStatus(c *gin.Context) {
	var request batchStatusRequest
	if !httpx.BindJSON(c, &request, "请选择用户并指定状态") {
		return
	}
	err := h.Users.BatchStatus(c.Request.Context(), request.IDs, *request.Enabled)
	if !h.mutate(c, err, actionUserBatchStatus, ResourceUser, "", "") {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) batchUserQuota(c *gin.Context) {
	var request batchQuotaRequest
	if !httpx.BindJSON(c, &request, "请选择用户并填写调整方式") {
		return
	}
	err := h.Users.BatchQuota(c.Request.Context(), request.IDs, serviceuser.TrafficInput{Mode: request.Mode, Bytes: request.Bytes})
	if !h.mutate(c, err, actionUserBatchQuota, ResourceUser, "", "") {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) batchUserProtocols(c *gin.Context) {
	var request batchProtocolsRequest
	if !httpx.BindJSON(c, &request, "请选择用户并指定分配方式") {
		return
	}
	err := h.Users.BatchProtocols(c.Request.Context(), request.IDs, request.Mode, request.ProtocolIDs)
	if !h.mutate(c, err, actionUserBatchProtocols, ResourceUser, "", "") {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) resetUserTraffic(c *gin.Context) {
	value, err := h.Users.ResetTraffic(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, actionUserTrafficReset, ResourceUser, value.ID, ResultSuccess)
	httpx.OK(c, value)
}

// adjustUser 收敛流量/额度调整：两者仅 service 方法与审计动作不同。
func (h *handler) adjustUser(c *gin.Context, action string, call func(context.Context, string, serviceuser.TrafficInput) (serviceuser.User, error)) {
	var request serviceuser.TrafficInput
	if !httpx.BindJSON(c, &request, "请选择方式并填写字节数") {
		return
	}
	value, err := call(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, action, ResourceUser, value.ID, ResultSuccess)
	httpx.OK(c, value)
}

func (h *handler) adjustUserQuota(c *gin.Context) {
	h.adjustUser(c, actionUserQuotaAdjust, h.Users.AdjustQuota)
}

func writeResourceError(c *gin.Context, err error) {
	var validation *common.ValidationError
	switch {
	case errors.As(err, &validation):
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "资源校验失败", validation.Fields)
	case errors.Is(err, common.ErrNotFound):
		httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "资源不存在", nil)
	case errors.Is(err, common.ErrRevisionConflict):
		httpx.Error(c, http.StatusConflict, httpx.CodeRevisionConflict, "资源已被修改", nil)
	case errors.Is(err, common.ErrReferenced):
		httpx.Error(c, http.StatusConflict, httpx.CodeResourceReferenced, "资源正在被引用", nil)
	case errors.Is(err, common.ErrDuplicate):
		httpx.Error(c, http.StatusConflict, httpx.CodeNameConflict, "名称已存在", map[string]string{"name": "已存在"})
	default:
		writeInternalError(c, "内部服务器错误")
	}
}
