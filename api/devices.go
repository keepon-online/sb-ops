package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

type deviceUpdateRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type deviceMutationResponse struct {
	Device     serviceuser.Device            `json:"device"`
	Credential *serviceuser.IssuedCredential `json:"credential,omitempty"`
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
	httpx.OK(c, gin.H{"items": devices, "subscriptionToken": token, "maxDevices": serviceuser.MaxDevicesPerUser})
}

// rotateUserSubscription re-issues the user-level subscription link.
func (h *handler) rotateUserSubscription(c *gin.Context) {
	token, err := h.Users.RotateSubscriptionToken(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, actionUserSubscriptionRotate, resourceUser, c.Param("id"), resultSuccess)
	httpx.OK(c, gin.H{"subscriptionToken": token})
}

func (h *handler) updateUserDevice(c *gin.Context) {
	var request deviceUpdateRequest
	if !httpx.BindJSON(c, &request, "设备信息无效") {
		return
	}
	err := h.Users.SetDeviceEnabled(c.Request.Context(), c.Param("id"), c.Param("did"), *request.Enabled)
	if !h.mutate(c, err, actionDeviceUpdate, resourceDevice, c.Param("id"), c.Param("did")) {
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
