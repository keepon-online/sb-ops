package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

type deviceCreateRequest struct {
	Name string `json:"name" binding:"required"`
}

type deviceUpdateRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

type deviceMutationResponse struct {
	Device     serviceuser.Device            `json:"device"`
	Credential *serviceuser.IssuedCredential `json:"credential,omitempty"`
}

func (h *handler) listUserDevices(c *gin.Context) {
	devices, err := h.users.ListDevices(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	token, err := h.users.SubscriptionToken(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, actionDeviceList, resourceDevice, c.Param("id"), resultSuccess)
	httpx.OK(c, gin.H{"items": devices, "subscriptionToken": token})
}

// rotateUserSubscription re-issues the user-level subscription link.
func (h *handler) rotateUserSubscription(c *gin.Context) {
	token, err := h.users.RotateSubscriptionToken(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	h.audit(c, actionUserSubscriptionRotate, resourceUser, c.Param("id"), resultSuccess)
	httpx.OK(c, gin.H{"subscriptionToken": token})
}

func (h *handler) createUserDevice(c *gin.Context) {
	var request deviceCreateRequest
	if !httpx.BindJSON(c, &request, "设备信息无效") {
		return
	}
	device, credential, err := h.users.CreateDevice(c.Request.Context(), c.Param("id"), serviceuser.DeviceInput{Name: request.Name})
	if !h.mutate(c, err, actionDeviceCreate, resourceDevice, c.Param("id"), device.ID) {
		return
	}
	httpx.Created(c, deviceMutationResponse{Device: device, Credential: &credential})
}

func (h *handler) updateUserDevice(c *gin.Context) {
	var request deviceUpdateRequest
	if !httpx.BindJSON(c, &request, "设备信息无效") {
		return
	}
	err := h.users.SetDeviceEnabled(c.Request.Context(), c.Param("id"), c.Param("did"), *request.Enabled)
	if !h.mutate(c, err, actionDeviceUpdate, resourceDevice, c.Param("id"), c.Param("did")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) deleteUserDevice(c *gin.Context) {
	err := h.users.DeleteDevice(c.Request.Context(), c.Param("id"), c.Param("did"))
	if !h.mutate(c, err, actionDeviceDelete, resourceDevice, c.Param("id"), c.Param("did")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) rotateUserDevice(c *gin.Context) {
	device, credential, err := h.users.RotateDevice(c.Request.Context(), c.Param("id"), c.Param("did"))
	if !h.mutate(c, err, actionDeviceRotate, resourceDevice, c.Param("id"), c.Param("did")) {
		return
	}
	httpx.OK(c, deviceMutationResponse{Device: device, Credential: &credential})
}
