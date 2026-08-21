package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
)

// syncTimeout bounds a detached kernel sync. The sync itself rebuilds the
// embedded sing-box and may download remote rule sets, so it needs generous
// headroom on a slow single-core VPS.
const syncTimeout = 5 * time.Minute

func (h *handler) synchronize(c *gin.Context, resourceType, resourceID string) bool {
	if h.sync == nil {
		return true
	}
	// 资源此刻已落库，同步是它的必然后续：客户端断开（网络抖动、关页
	// 面、 impatient 重试）不得中止同步。此前绑定请求 context，连接一断
	// 同步即 canceled——表现为「添加失败」，刷新后资源却又出现（未应用
	// 到内核）。用剥离取消的 context 继续跑完，超时兜底。
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), syncTimeout)
	defer cancel()
	if _, err := h.sync.Sync(syncCtx); err != nil {
		h.audit(c, actionRuntimeSync, resourceType, resourceID, resultFailure, err)
		slog.Error("内嵌 sing-box 同步失败", "error", err, "resourceType", resourceType, "resourceId", resourceID)
		httpx.Error(c, http.StatusConflict, httpx.CodeSyncFailed, "内嵌 sing-box 同步失败，上一份成功配置仍在运行", nil)
		return false
	}
	h.audit(c, actionRuntimeSync, resourceType, resourceID, resultSuccess)
	return true
}

// mutate 处理一次资源写操作的审计与同步收尾：失败以 failureID 记审计并写
// 错误响应，成功以 successID 记审计并触发内嵌内核同步。返回 true 表示
// handler 应继续写成功响应。所有会改变内核配置的写操作都必须经此收尾，
// 避免新增资源类型时漏掉审计或同步。
func (h *handler) mutate(c *gin.Context, err error, action, resourceType, failureID, successID string) bool {
	if err != nil {
		h.audit(c, action, resourceType, failureID, resultFailure, err)
		writeResourceError(c, err)
		return false
	}
	h.audit(c, action, resourceType, successID, resultSuccess)
	return h.synchronize(c, resourceType, successID)
}
