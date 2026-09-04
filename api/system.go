package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
)

// getUpgradeStatus 返回自升级服务快照；只读、不会失败，无需新增业务码。
func (h *handler) getUpgradeStatus(c *gin.Context) {
	httpx.OK(c, h.Upgrade.Status())
}

// checkUpgrade 同步查询 GitHub 最新 Release（升级进行中会被拒绝）。
func (h *handler) checkUpgrade(c *gin.Context) {
	release, err := h.Upgrade.Check(c.Request.Context())
	if err != nil {
		h.audit(c, actionSystemUpgradeCheck, resourceSystemUpgrade, "", resultFailure, err)
		writeUpgradeError(c, err)
		return
	}
	h.audit(c, actionSystemUpgradeCheck, resourceSystemUpgrade, release.Version, ResultSuccess)
	httpx.OK(c, release)
}

// startUpgrade 校验守卫后把下载/校验/替换放到后台执行并立即返回；面板
// 重生由升级服务经 restart 钩子触发，响应里只回当前阶段。
func (h *handler) startUpgrade(c *gin.Context) {
	if err := h.Upgrade.Start(c.Request.Context()); err != nil {
		h.audit(c, actionSystemUpgradeStart, resourceSystemUpgrade, "", resultFailure, err)
		writeUpgradeError(c, err)
		return
	}
	resourceID := ""
	if latest := h.Upgrade.Status().Latest; latest != nil {
		resourceID = latest.Version
	}
	h.audit(c, actionSystemUpgradeStart, resourceSystemUpgrade, resourceID, ResultSuccess)
	httpx.OK(c, h.Upgrade.Status())
}

func writeUpgradeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, controlupgrade.ErrNotConfigured):
		httpx.Error(c, http.StatusBadRequest, httpx.CodeUpgradeNotConfigured, err.Error(), nil)
	case errors.Is(err, controlupgrade.ErrBusy):
		httpx.Error(c, http.StatusConflict, httpx.CodeUpgradeBusy, err.Error(), nil)
	case errors.Is(err, controlupgrade.ErrAlreadyLatest):
		httpx.Error(c, http.StatusBadRequest, httpx.CodeUpgradeAlreadyLatest, err.Error(), nil)
	default:
		// 前台错误只剩 GitHub 查询失败；下载/替换失败发生在后台，经
		// status.phase=failed 与 upgrade.failed 事件暴露。
		httpx.Error(c, http.StatusBadGateway, httpx.CodeUpgradeCheckFailed, "查询 GitHub 最新版本失败，请稍后重试", nil)
	}
}
