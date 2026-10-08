package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	servicecommon "github.com/keepon-online/sb-ops/internal/service/common"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

type deviceCreateRequest struct {
	Name string `json:"name" binding:"required"`
}

type deviceUpdateRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
}

type deviceMutationResponse struct {
	Device     serviceuser.Device            `json:"device"`
	Credential *serviceuser.IssuedCredential `json:"credential,omitempty"`
}

// deviceListItem 给设备行附带历史累计流量：来自 traffic_hourly 事实表，
// 不随额度周期清零（与 usedBytes 的本周期计数口径互补）。
type deviceListItem struct {
	serviceuser.Device
	LifetimeBytes int64 `json:"lifetimeBytes"`
}

func (h *handler) createUserDevice(c *gin.Context) {
	var request deviceCreateRequest
	if !httpx.BindJSON(c, &request, "设备信息无效") {
		return
	}
	device, credential, err := h.Users.CreateDevice(c.Request.Context(), c.Param("id"), request.Name)
	if !h.mutate(c, err, actionDeviceCreate, resourceDevice, c.Param("id"), device.ID) {
		return
	}
	httpx.Created(c, deviceMutationResponse{Device: device, Credential: &credential})
}

func (h *handler) listUserDevices(c *gin.Context) {
	devices, err := h.Users.ListDevices(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	token, err := h.Users.SubscriptionToken(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	// 历史累计为展示增强：聚合失败时省略该维度（字段缺省），不影响设备列表。
	items := make([]deviceListItem, 0, len(devices))
	for index := range devices {
		items = append(items, deviceListItem{Device: devices[index]})
	}
	var userLifetime *int64
	if h.Traffic != nil {
		if lifetimes, lifetimeErr := h.Traffic.DeviceLifetimes(c.Request.Context(), c.Param("id")); lifetimeErr == nil {
			var total int64
			for _, value := range lifetimes {
				total += value
			}
			userLifetime = &total
			for index := range items {
				items[index].LifetimeBytes = lifetimes[items[index].ID]
			}
		}
	}
	httpx.OK(c, gin.H{
		"items":               items,
		"subscriptionToken":   token,
		"maxDevices":          serviceuser.MaxDevicesPerUser,
		"subscriptionBaseUrl": h.subscriptionBaseURL(c),
		"userLifetimeBytes":   userLifetime,
	})
}

// rotateUserSubscription re-issues the user-level subscription link.
func (h *handler) rotateUserSubscription(c *gin.Context) {
	token, err := h.Users.RotateSubscriptionToken(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, actionUserSubscriptionRotate, ResourceUser, c.Param("id"), ResultSuccess)
	httpx.OK(c, gin.H{
		"subscriptionToken":   token,
		"subscriptionBaseUrl": h.subscriptionBaseURL(c),
	})
}

func (h *handler) updateUserDevice(c *gin.Context) {
	var request deviceUpdateRequest
	if !httpx.BindJSON(c, &request, "设备信息无效") {
		return
	}
	if request.Name == nil && request.Enabled == nil {
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "至少需要提供一个更新字段", nil)
		return
	}
	before, ok, beforeErr := h.Users.FindDeviceByID(c.Request.Context(), c.Param("id"), c.Param("did"))
	if beforeErr == nil && !ok {
		beforeErr = servicecommon.ErrNotFound
	}
	after, err := h.Users.UpdateDevice(c.Request.Context(), c.Param("id"), c.Param("did"), request.Name, request.Enabled)
	if !h.mutate(c, err, actionDeviceUpdate, resourceDevice, c.Param("id"), c.Param("did"), diffable(beforeErr, err, before, after)) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) deleteUserDevice(c *gin.Context) {
	err := h.Users.DeleteDevice(c.Request.Context(), c.Param("id"), c.Param("did"))
	if !h.mutate(c, err, actionDeviceDelete, resourceDevice, c.Param("id"), c.Param("did")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) rotateUserDevice(c *gin.Context) {
	device, credential, err := h.Users.RotateDevice(c.Request.Context(), c.Param("id"), c.Param("did"))
	if !h.mutate(c, err, actionDeviceRotate, resourceDevice, c.Param("id"), c.Param("did")) {
		return
	}
	httpx.OK(c, deviceMutationResponse{Device: device, Credential: &credential})
}
