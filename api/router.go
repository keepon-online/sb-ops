package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/config"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	controlmonitor "github.com/keepon-online/sb-ops/internal/monitor"
	controlobservability "github.com/keepon-online/sb-ops/internal/observability"
	controloverview "github.com/keepon-online/sb-ops/internal/overview"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	common "github.com/keepon-online/sb-ops/internal/service/common"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceshare "github.com/keepon-online/sb-ops/internal/service/share"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
)

const (
	principalKey = "identity_principal"
	tokenKey     = "identity_token"
)

type Dependencies struct {
	Identity     *identity.Service
	Store        *identity.Store
	Users        *serviceuser.Service
	Protocols    *serviceprotocol.Service
	Certificates *servicecertificate.Service
	Policies     *servicepolicy.Service
	SingBox      *servicesingbox.Service
	Sync         *deployment.SyncService
	Overview     *controloverview.Service
	AuditLogs    *controlaudit.Service
	Share        *serviceshare.Service
	Traffic      *controltraffic.Service
	Events       *controlevents.Bus
	Alerts       *controlobservability.Service
	Monitor      *controlmonitor.Service
	Config       func() config.RuntimeConfig
	Argo         *serviceargo.Service
	// ArgoManager restarts tunnel processes; the supervisor may be absent in
	// tests, hence the narrow interface.
	ArgoManager interface{ RestartTunnel(id string) }
}

type handler struct {
	identity     *identity.Service
	store        *identity.Store
	users        *serviceuser.Service
	protocols    *serviceprotocol.Service
	certificates *servicecertificate.Service
	policies     *servicepolicy.Service
	singbox      *servicesingbox.Service
	sync         *deployment.SyncService
	overview     *controloverview.Service
	auditLogs    *controlaudit.Service
	share        *serviceshare.Service
	traffic      *controltraffic.Service
	events       *controlevents.Bus
	alerts       *controlobservability.Service
	monitor      *controlmonitor.Service
	config       func() config.RuntimeConfig
	argo         *serviceargo.Service
	argoManager  interface{ RestartTunnel(id string) }
}

func RegisterRoutes(router *gin.Engine, dependencies Dependencies) {
	configProvider := dependencies.Config
	if configProvider == nil {
		configProvider = func() config.RuntimeConfig { return config.DefaultRuntimeConfig() }
	}
	h := &handler{
		identity: dependencies.Identity, store: dependencies.Store, users: dependencies.Users,
		protocols: dependencies.Protocols, certificates: dependencies.Certificates,
		policies: dependencies.Policies, singbox: dependencies.SingBox, argo: dependencies.Argo, argoManager: dependencies.ArgoManager,
		sync:     dependencies.Sync,
		overview: dependencies.Overview, auditLogs: dependencies.AuditLogs, share: dependencies.Share, traffic: dependencies.Traffic, events: dependencies.Events, alerts: dependencies.Alerts,
		monitor: dependencies.Monitor,
		config:  configProvider,
	}
	if h.share != nil {
		router.GET("/subscribe/:token/:format", h.downloadSubscription)
	}
	api := router.Group("/api/v1")
	api.GET("/setup/status", h.setupStatus)
	api.POST("/setup/admin", h.initializeAdmin)
	api.POST("/auth/login", h.login)

	authenticated := api.Group("")
	authenticated.Use(h.authenticate())
	authenticated.GET("/auth/me", h.me)
	authenticated.POST("/auth/change-password", h.changePassword)
	authenticated.POST("/auth/logout", h.logout)
	if h.events != nil {
		authenticated.GET("/events", h.streamEvents)
	}

	if h.users != nil {
		users := authenticated.Group("/users")
		users.GET("", h.listUsers)
		users.GET("/:id", h.getUser)
		users.POST("", h.createUser)
		users.PATCH("/:id", h.updateUser)
		users.DELETE("/:id", h.deleteUser)
		users.POST("/batch-status", h.batchUserStatus)

		userSubscription := authenticated.Group("/users/:id/subscription")
		userSubscription.POST("/rotate", h.rotateUserSubscription)

		devices := authenticated.Group("/users/:id/devices")
		devices.GET("", h.listUserDevices)
		devices.PATCH("/:did", h.updateUserDevice)
		devices.DELETE("/:did", h.deleteUserDevice)
		devices.POST("/:did/credentials/rotate", h.rotateUserDevice)

		traffic := authenticated.Group("/users")
		traffic.POST("/:id/traffic/reset", h.resetUserTraffic)
		traffic.POST("/:id/traffic/adjust", h.adjustUserTraffic)
		traffic.POST("/:id/quota/adjust", h.adjustUserQuota)
	}

	if h.protocols != nil && h.certificates != nil {
		resourceRead := authenticated.Group("")
		resourceRead.GET("/protocol-schemas/:type", h.protocolSchema)
		resourceRead.GET("/protocols", h.listProtocols)
		resourceRead.GET("/protocols/runtime-status", h.protocolRuntimeStatus)
		resourceRead.GET("/protocols/:id", h.getProtocol)
		resourceRead.GET("/certificates", h.listCertificates)
		resourceRead.GET("/certificates/:id", h.getCertificate)

		resourceWrite := authenticated.Group("")
		resourceWrite.POST("/protocols/suggest-port", h.suggestProtocolPort)
		resourceWrite.POST("/protocols", h.createProtocol)
		resourceWrite.PATCH("/protocols/:id", h.updateProtocol)
		resourceWrite.DELETE("/protocols/:id", h.deleteProtocol)
		resourceWrite.POST("/protocols/:id/clone", h.cloneProtocol)
		resourceWrite.POST("/protocols/:id/rotate-secret", h.rotateProtocolSecret)
		resourceWrite.POST("/certificates/self-signed", h.createSelfSignedCertificate)
		resourceWrite.POST("/certificates/acme", h.createAcmeCertificate)
		resourceWrite.POST("/certificates/:id/regenerate", h.regenerateSelfSignedCertificate)
		resourceWrite.POST("/certificates/:id/renew", h.renewAcmeCertificate)
		resourceWrite.POST("/certificates", h.createCertificate)
		resourceWrite.PATCH("/certificates/:id", h.updateCertificate)
		resourceWrite.DELETE("/certificates/:id", h.deleteCertificate)
	}

	if h.policies != nil {
		policyRead := authenticated.Group("/policies")
		policyRead.GET("", h.listPolicies)
		policyRead.GET("/rule-sets", h.listPolicyRuleSets)
		policyRead.GET("/:id", h.getPolicy)
		policyRead.GET("/:id/rules", h.listPolicyRules)
		policyRead.POST("/:id/dry-run", h.dryRunPolicy)
		policyRead.POST("/:id/validate", h.validatePolicy)

		policyWrite := authenticated.Group("/policies")
		policyWrite.POST("", h.createPolicy)
		policyWrite.PATCH("/:id", h.updatePolicy)
		policyWrite.DELETE("/:id", h.deletePolicy)
		policyWrite.POST("/:id/clone", h.clonePolicy)
		policyWrite.POST("/:id/rules", h.createPolicyRule)
		policyWrite.PATCH("/:id/rules/:ruleId", h.updatePolicyRule)
		policyWrite.DELETE("/:id/rules/:ruleId", h.deletePolicyRule)
		policyWrite.PUT("/:id/rules/order", h.orderPolicyRules)
	}

	if h.singbox != nil {
		singBoxRead := authenticated.Group("/singbox")
		singBoxRead.GET("", h.getSingBoxConfig)
		singBoxRead.GET("/runtime", h.getSingBoxRuntime)
		singBoxRead.GET("/connections", h.listSingBoxConnections)
		singBoxRead.GET("/outbounds", h.listSingBoxOutbounds)
		singBoxRead.GET("/route-rules", h.listSingBoxRouteRules)
		singBoxRead.GET("/rule-sets", h.listSingBoxRuleSets)
		singBoxRead.GET("/argos", h.listArgoTunnels)
		singBoxRead.POST("/route-rules/dry-run", h.dryRunSingBoxRoutes)
		singBoxRead.POST("/validate", h.validateSingBox)

		singBoxWrite := authenticated.Group("/singbox")
		singBoxWrite.PATCH("", h.updateSingBoxConfig)
		singBoxWrite.POST("/outbounds", h.createSingBoxOutbound)
		singBoxWrite.PATCH("/outbounds/:id", h.updateSingBoxOutbound)
		singBoxWrite.DELETE("/outbounds/:id", h.deleteSingBoxOutbound)
		singBoxWrite.POST("/outbounds/warp/register", h.registerSingBoxWarp)
		singBoxWrite.POST("/argos", h.createArgoTunnel)
		singBoxWrite.PATCH("/argos/:id", h.updateArgoTunnel)
		singBoxWrite.DELETE("/argos/:id", h.deleteArgoTunnel)
		singBoxWrite.POST("/argos/:id/restart", h.restartArgoTunnel)
		singBoxWrite.POST("/route-rules", h.createSingBoxRouteRule)
		singBoxWrite.POST("/route-rules/ai-template", h.applySingBoxAITemplate)
		singBoxWrite.PATCH("/route-rules/:id", h.updateSingBoxRouteRule)
		singBoxWrite.DELETE("/route-rules/:id", h.deleteSingBoxRouteRule)
		singBoxWrite.PUT("/route-rules/order", h.orderSingBoxRouteRules)
		singBoxWrite.POST("/rule-sets", h.createSingBoxRuleSet)
		singBoxWrite.PATCH("/rule-sets/:id", h.updateSingBoxRuleSet)
		singBoxWrite.DELETE("/rule-sets/:id", h.deleteSingBoxRuleSet)

		runtimeOperate := authenticated.Group("/singbox")
		runtimeOperate.POST("/check", h.checkSingBoxRuntime)
		runtimeOperate.POST("/reconcile", h.reconcileSingBoxRuntime)
		runtimeOperate.POST("/restart", h.restartSingBoxRuntime)
		runtimeOperate.DELETE("/connections/:id", h.closeSingBoxConnection)
	}

	if h.overview != nil {
		overview := authenticated.Group("")
		overview.GET("/overview", h.getOverview)
	}
	if h.monitor != nil {
		monitor := authenticated.Group("")
		monitor.GET("/monitor", h.getMonitorStatus)
	}
	if h.auditLogs != nil {
		auditLogs := authenticated.Group("")
		auditLogs.GET("/audit-logs", h.listAuditLogs)
	}
	if h.alerts != nil {
		alertsRead := authenticated.Group("/alerts")
		alertsRead.GET("", h.listAlerts)
		alertsWrite := authenticated.Group("/alerts")
		alertsWrite.POST("/:id/acknowledge", h.acknowledgeAlert)
	}
	if h.traffic != nil {
		trafficRead := authenticated.Group("/traffic")
		trafficRead.GET("/series", h.trafficSeries)
		trafficRead.GET("/rankings", h.trafficRankings)
		trafficRead.GET("/summary", h.trafficSummary)
		trafficRead.GET("/active-users", h.trafficActiveUsers)
		trafficRead.GET("/protocol-shares", h.trafficProtocolShares)
	}
}

func (h *handler) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		principal, err := h.identity.Authenticate(c.Request.Context(), token)
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
	if h.store == nil {
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
	detailsJSON := "{}"
	if len(errDetail) > 0 && errDetail[0] != nil {
		if details := marshalErrorDetails(errDetail[0]); details != "" {
			detailsJSON = details
		}
	}
	if err := h.store.AppendAuditLog(auditCtx, identity.AuditLog{
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

func clientAddress(c *gin.Context) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(c.Request.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(c.Request.RemoteAddr)
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
