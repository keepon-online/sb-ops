package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	common "github.com/keepon-online/sb-ops/internal/service/common"
)

// itemsResponse 是各资源列表的同形响应包装（wire 格式 {"items": [...]}）。
type itemsResponse[T any] struct {
	Items []T `json:"items"`
}

func (h *handler) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		principal, err := h.Identity.Authenticate(c.Request.Context(), token)
		if err != nil {
			httpx.Error(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "请先登录", nil)
			return
		}
		c.Set(principalKey, principal)
		c.Set(tokenKey, token)
		c.Next()
	}
}

func (h *handler) audit(c *gin.Context, action, resourceType, resourceID, result string, errDetail ...error) {
	detailsJSON := "{}"
	if len(errDetail) > 0 && errDetail[0] != nil {
		if details := marshalErrorDetails(errDetail[0]); details != "" {
			detailsJSON = details
		}
	}
	h.appendAuditLog(c, action, resourceType, resourceID, result, detailsJSON)
}

// auditChanges 落一条带字段级 diff 的审计：details 形如
// {"changes":{"defaultAction":{"old":"direct","new":"proxy"}}}，让更新类
// 操作可追溯「改了什么」——此前成功审计只留 {}，排查只能靠猜。
func (h *handler) auditChanges(c *gin.Context, action, resourceType, resourceID string, changes map[string]any) {
	payload, err := json.Marshal(map[string]any{"changes": changes})
	if err != nil {
		h.appendAuditLog(c, action, resourceType, resourceID, ResultSuccess, "{}")
		return
	}
	h.appendAuditLog(c, action, resourceType, resourceID, ResultSuccess, string(payload))
}

func (h *handler) appendAuditLog(c *gin.Context, action, resourceType, resourceID, result, detailsJSON string) {
	if h.AuditLogs == nil {
		return
	}
	// 审计写库剥离请求取消：客户端断开后（同步收尾阶段常见）审计仍必须
	// 落盘，否则合规追溯静默失效。
	auditCtx := context.WithoutCancel(c.Request.Context())
	var actorID *string
	if value, ok := c.Get(principalKey); ok {
		principal := value.(identity.Principal)
		actorID = &principal.AccountID
	}
	if err := h.AuditLogs.Append(auditCtx, controlaudit.Record{
		ID: uuid.NewString(), ActorID: actorID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Result: result,
		RequestID: httpx.RequestID(c), ClientIP: clientAddress(c), DetailsJSON: detailsJSON,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		// 审计写入在业务事务之外，失败不影响业务结果，但必须留痕，
		// 否则合规追溯能力会静默失效。
		slog.Error("写入审计日志失败",
			"error", err, "action", action, "resourceType", resourceType,
			"resourceId", resourceID, "requestId", httpx.RequestID(c),
		)
	}
}

func marshalErrorDetails(err error) string {
	type fieldError struct {
		Message string            `json:"message,omitempty"`
		Fields  map[string]string `json:"fields,omitempty"`
	}
	var fe *common.ValidationError
	if errors.As(err, &fe) {
		b, _ := json.Marshal(fieldError{Message: err.Error(), Fields: fe.Fields})
		return string(b)
	}
	if err.Error() != "" {
		b, _ := json.Marshal(fieldError{Message: err.Error()})
		return string(b)
	}
	return ""
}

func currentPrincipal(c *gin.Context) identity.Principal {
	value, _ := c.Get(principalKey)
	principal, _ := value.(identity.Principal)
	return principal
}

func bearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

// clientAddress 返回请求来源地址，供登录限速键与审计使用。经
// RegisterRoutes 的 SetTrustedProxies 限定：反代（回环 Nginx）注入的
// X-Forwarded-For / X-Real-IP 才被采信，直连时即对端地址。勿退回裸
// RemoteAddr——固定反代部署下它恒为 127.0.0.1，登录限速会退化成全局桶
// （未认证者可锁死管理员登录）。
func clientAddress(c *gin.Context) string {
	return c.ClientIP()
}

// writeInternalError 收敛 500 INTERNAL_ERROR 响应；message 描述失败的操作上下文。
func writeInternalError(c *gin.Context, message string) {
	httpx.Error(c, http.StatusInternalServerError, httpx.CodeInternalError, message, nil)
}

func writeIdentityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		httpx.Error(c, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "用户名或密码错误", nil)
	case errors.Is(err, identity.ErrUnauthorized):
		httpx.Error(c, http.StatusUnauthorized, httpx.CodeUnauthorized, "请先登录", nil)
	case errors.Is(err, identity.ErrRateLimited):
		httpx.Error(c, http.StatusTooManyRequests, httpx.CodeRateLimited, "登录尝试过于频繁，请稍后再试", nil)
	case errors.Is(err, identity.ErrPasswordPolicy):
		httpx.Error(c, http.StatusBadRequest, httpx.CodePasswordPolicy, "密码不符合要求", map[string]string{"password": "至少 10 位且须包含字母和数字"})
	case errors.Is(err, identity.ErrInvalidAccountInput):
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "账号信息无效", nil)
	case errors.Is(err, identity.ErrRevisionConflict):
		httpx.Error(c, http.StatusConflict, httpx.CodeRevisionConflict, "资源已被修改", nil)
	case errors.Is(err, sql.ErrNoRows):
		httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "资源不存在", nil)
	case strings.Contains(strings.ToLower(err.Error()), "unique constraint"):
		httpx.Error(c, http.StatusConflict, httpx.CodeUsernameConflict, "用户名已存在", map[string]string{"username": "已存在"})
	default:
		writeInternalError(c, "内部服务器错误")
	}
}
