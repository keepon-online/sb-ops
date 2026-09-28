package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
	controlwarphealth "github.com/keepon-online/sb-ops/internal/warphealth"
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

// rotateWarp 执行 WARP 一键换出口（warp-cli 断开重连 + 立即复测），照
// backup/upgrade 先例 h.audit 直记 + httpx 响应（不接 h.mutate——不涉
// 内核重编译）。换出口对链路是数秒级中断，由前端二次确认。
func (h *handler) rotateWarp(c *gin.Context) {
	if h.WarpRotate == nil {
		httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeWarpRotateUnavailable, "换出口功能未接线", nil)
		return
	}
	result, err := h.WarpRotate(c.Request.Context())
	if err != nil {
		h.audit(c, actionSystemWarpRotate, resourceSystemWarp, "", resultFailure, err)
		writeWarpRotateError(c, err)
		return
	}
	h.audit(c, actionSystemWarpRotate, resourceSystemWarp, result.Verdict, ResultSuccess)
	httpx.OK(c, result)
}

func writeWarpRotateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, controlwarphealth.ErrWarpNotDeployed):
		httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "未部署 WARP 解锁链路（缺少 warp-masque 出站）", nil)
	default:
		httpx.Error(c, http.StatusBadGateway, httpx.CodeWarpRotateFailed, err.Error(), nil)
	}
}

// warpClientStatus 返回 WARP 客户端（官方 cloudflare-warp 包）的完整状态
// 视图：系统侧（软件包/服务/注册/模式）+ 面板侧联动（出站存在性、链路判定）
// + 安装阶段机。只读无副作用，不审计。
func (h *handler) warpClientStatus(c *gin.Context) {
	if h.WarpClient == nil {
		httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeWarpClientUnavailable, "WARP 客户端管理未接线", nil)
		return
	}
	httpx.OK(c, h.WarpClient.Status(c.Request.Context()))
}

// installWarpClient 启动后台安装（添加软件源 → 安装软件包 → 配置并连接），
// 立即返回当前阶段快照；进度与失败经 status 端点轮询观察。
func (h *handler) installWarpClient(c *gin.Context) {
	if h.WarpClient == nil {
		httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeWarpClientUnavailable, "WARP 客户端管理未接线", nil)
		return
	}
	if err := h.WarpClient.Install(c.Request.Context()); err != nil {
		h.audit(c, actionSystemWarpInstall, resourceSystemWarp, "", resultFailure, err)
		writeWarpClientError(c, err)
		return
	}
	h.audit(c, actionSystemWarpInstall, resourceSystemWarp, controlwarphealth.PhaseAddingRepo, ResultSuccess)
	httpx.OK(c, h.WarpClient.Status(c.Request.Context()))
}

// reconfigureWarpClient 幂等执行配置序列（服务自启 + 注册 + proxy 模式 +
// 端口 + 连接），同步返回执行后的最新状态。
func (h *handler) reconfigureWarpClient(c *gin.Context) {
	if h.WarpClient == nil {
		httpx.Error(c, http.StatusServiceUnavailable, httpx.CodeWarpClientUnavailable, "WARP 客户端管理未接线", nil)
		return
	}
	if err := h.WarpClient.Reconfigure(c.Request.Context()); err != nil {
		h.audit(c, actionSystemWarpReconfigure, resourceSystemWarp, "", resultFailure, err)
		writeWarpClientError(c, err)
		return
	}
	h.audit(c, actionSystemWarpReconfigure, resourceSystemWarp, "reconfigured", ResultSuccess)
	httpx.OK(c, h.WarpClient.Status(c.Request.Context()))
}

func writeWarpClientError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, controlwarphealth.ErrInstallBusy):
		httpx.Error(c, http.StatusConflict, httpx.CodeWarpInstallBusy, err.Error(), nil)
	case errors.Is(err, controlwarphealth.ErrWarpClientNotInstalled):
		httpx.Error(c, http.StatusNotFound, httpx.CodeWarpClientNotInstalled, err.Error(), nil)
	default:
		httpx.Error(c, http.StatusBadGateway, httpx.CodeWarpReconfigureFailed, err.Error(), nil)
	}
}
