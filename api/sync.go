package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
)

// syncTimeout bounds a detached kernel sync. The sync itself rebuilds the
// embedded sing-box and may download remote rule sets, so it needs generous
// headroom on a slow single-core VPS.
const syncTimeout = 5 * time.Minute

func (h *handler) synchronize(c *gin.Context, resourceType, resourceID string) bool {
	if h.Sync == nil {
		return true
	}
	// 资源此刻已落库，同步是它的必然后续：客户端断开（网络抖动、关页
	// 面、 impatient 重试）不得中止同步。此前绑定请求 context，连接一断
	// 同步即 canceled——表现为「添加失败」，刷新后资源却又出现（未应用
	// 到内核）。用剥离取消的 context 继续跑完，超时兜底。
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), syncTimeout)
	defer cancel()
	if _, err := h.Sync.Sync(syncCtx); err != nil {
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
// 避免新增资源类型时漏掉审计或同步。更新类操作可额外传一份字段级 diff
// （resourceChanges 的返回值），成功审计的 details 会带上 {old,new} 对比。
func (h *handler) mutate(c *gin.Context, err error, action, resourceType, failureID, successID string, changes ...map[string]any) bool {
	if err != nil {
		h.audit(c, action, resourceType, failureID, resultFailure, err)
		writeResourceError(c, err)
		return false
	}
	if len(changes) > 0 && len(changes[0]) > 0 {
		h.auditChanges(c, action, resourceType, successID, changes[0])
	} else {
		h.audit(c, action, resourceType, successID, resultSuccess)
	}
	return h.synchronize(c, resourceType, successID)
}

// resourceChanges 计算资源改前/改后快照（按 JSON 视图）的字段级差异，
// 忽略 revision / updatedAt 这类必然变化的簿记字段；无实际差异返回 nil。
func resourceChanges(before, after any) map[string]any {
	beforeJSON, beforeErr := json.Marshal(before)
	afterJSON, afterErr := json.Marshal(after)
	if beforeErr != nil || afterErr != nil {
		return nil
	}
	var beforeMap, afterMap map[string]any
	if json.Unmarshal(beforeJSON, &beforeMap) != nil || json.Unmarshal(afterJSON, &afterMap) != nil {
		return nil
	}
	bookkeeping := map[string]bool{"revision": true, "updatedAt": true}
	changes := map[string]any{}
	for field, newValue := range afterMap {
		if bookkeeping[field] {
			continue
		}
		oldValue, existed := beforeMap[field]
		if existed && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		if existed {
			changes[field] = map[string]any{"old": oldValue, "new": newValue}
		} else {
			changes[field] = map[string]any{"new": newValue}
		}
	}
	for field, oldValue := range beforeMap {
		if bookkeeping[field] {
			continue
		}
		if _, exists := afterMap[field]; !exists {
			changes[field] = map[string]any{"old": oldValue}
		}
	}
	if len(changes) == 0 {
		return nil
	}
	return changes
}

// diffable 在改前快照取到了且更新成功时计算字段 diff，否则返回 nil——
// 更新 handler 用它把「取旧值可能失败」的分支收敛成一次调用。
func diffable[T any](beforeErr, err error, before, after T) map[string]any {
	if beforeErr != nil || err != nil {
		return nil
	}
	return resourceChanges(before, after)
}
