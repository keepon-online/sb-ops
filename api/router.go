package api

import (
	"context"

	"github.com/gin-gonic/gin"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/config"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/identity"
	controlmonitor "github.com/keepon-online/sb-ops/internal/monitor"
	controlobservability "github.com/keepon-online/sb-ops/internal/observability"
	controloverview "github.com/keepon-online/sb-ops/internal/overview"
	controlprobe "github.com/keepon-online/sb-ops/internal/probe"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicebackup "github.com/keepon-online/sb-ops/internal/service/backup"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceshare "github.com/keepon-online/sb-ops/internal/service/share"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
	controlwarphealth "github.com/keepon-online/sb-ops/internal/warphealth"
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
	Upgrade      *controlupgrade.Service
	Backup       *servicebackup.Service
	// Prober 跑协议端到端拨测；测试可缺省（对应端点返回 503）。
	Prober *controlprobe.Prober
	// WarpPrechecker 对新注册的免费 WARP 身份做出口预检（一次性客户端内核
	// 实测出口 IP 与 Google 判定）；测试可缺省（注册响应不带 precheck 字段）。
	WarpPrechecker *controlprobe.WarpPrechecker
	// WarpRotate 执行 WARP 一键换出口（sudo warp-cli 断开重连 + 立即复测）；
	// 测试可缺省（对应端点返回 503，同 Prober 先例）。
	WarpRotate func(context.Context) (controlwarphealth.Result, error)
	// ArgoManager restarts tunnel processes; the supervisor may be absent in
	// tests, hence the narrow interface.
	ArgoManager interface{ RestartTunnel(id string) }
}

// handler 直接以 Dependencies 为载体：字段清单只在 Dependencies 一处维护，
// 新增服务不再需要 handler 镜像拷贝。
type handler Dependencies

func RegisterRoutes(router *gin.Engine, dependencies Dependencies) {
	if dependencies.Config == nil {
		dependencies.Config = func() config.RuntimeConfig { return config.DefaultRuntimeConfig() }
	}
	// 只信任同机反代（Nginx → 127.0.0.1）：gin 默认信任所有代理，XFF 最左值
	// 完全由客户端控制，会让订阅设备指纹的 IP 维度可任意伪造；限定后
	// c.ClientIP() 仅在直连方为回环时采信转发头，直连场景仍取 RemoteAddr。
	if err := router.SetTrustedProxies([]string{"127.0.0.1", "::1"}); err != nil {
		panic("配置可信代理失败: " + err.Error())
	}
	h := handler(dependencies)
	h.registerPublicRoutes(router)

	api := router.Group("/api/v1")
	api.GET("/setup/status", h.setupStatus)
	api.POST("/setup/admin", h.initializeAdmin)
	api.POST("/auth/login", h.login)

	authenticated := api.Group("")
	authenticated.Use(h.authenticate())
	authenticated.GET("/auth/me", h.me)
	authenticated.POST("/auth/change-password", h.changePassword)
	authenticated.POST("/auth/logout", h.logout)
	if h.Events != nil {
		authenticated.GET("/events", h.streamEvents)
	}

	if h.Users != nil {
		users := authenticated.Group("/users")
		users.GET("", h.listUsers)
		users.GET("/:id", h.getUser)
		users.POST("", h.createUser)
		users.PATCH("/:id", h.updateUser)
		users.DELETE("/:id", h.deleteUser)
		users.POST("/batch-status", h.batchUserStatus)
		users.POST("/batch-quota", h.batchUserQuota)
		users.POST("/batch-protocols", h.batchUserProtocols)

		userSubscription := authenticated.Group("/users/:id/subscription")
		userSubscription.POST("/rotate", h.rotateUserSubscription)

		devices := authenticated.Group("/users/:id/devices")
		devices.GET("", h.listUserDevices)
		devices.POST("", h.createUserDevice)
		devices.PATCH("/:did", h.updateUserDevice)
		devices.DELETE("/:did", h.deleteUserDevice)
		devices.POST("/:did/credentials/rotate", h.rotateUserDevice)

		traffic := authenticated.Group("/users")
		traffic.POST("/:id/traffic/reset", h.resetUserTraffic)
		traffic.POST("/:id/quota/adjust", h.adjustUserQuota)
	}

	if h.Protocols != nil && h.Certificates != nil {
		resourceRead := authenticated.Group("")
		resourceRead.GET("/protocol-schemas/:type", h.protocolSchema)
		resourceRead.GET("/protocols", h.listProtocols)
		resourceRead.GET("/protocols/runtime-status", h.protocolRuntimeStatus)
		resourceRead.GET("/protocols/probe-results", h.protocolProbeResults)
		resourceRead.GET("/protocols/:id", h.getProtocol)
		resourceRead.GET("/certificates", h.listCertificates)
		resourceRead.GET("/certificates/:id", h.getCertificate)

		resourceWrite := authenticated.Group("")
		resourceWrite.POST("/protocols/suggest-port", h.suggestProtocolPort)
		resourceWrite.POST("/protocols/probe", h.runProtocolProbe)
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

	if h.Policies != nil {
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

	if h.SingBox != nil {
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

	if h.Overview != nil {
		overview := authenticated.Group("")
		overview.GET("/overview", h.getOverview)
	}
	if h.Monitor != nil {
		monitor := authenticated.Group("")
		monitor.GET("/monitor", h.getMonitorStatus)
	}
	if h.AuditLogs != nil {
		auditLogs := authenticated.Group("")
		auditLogs.GET("/audit-logs", h.listAuditLogs)
	}
	if h.Alerts != nil {
		alertsRead := authenticated.Group("/alerts")
		alertsRead.GET("", h.listAlerts)
		alertsRead.GET("/stats", h.alertStats)
		alertsWrite := authenticated.Group("/alerts")
		alertsWrite.POST("/:id/acknowledge", h.acknowledgeAlert)
	}
	if h.Traffic != nil {
		trafficRead := authenticated.Group("/traffic")
		trafficRead.GET("/series", h.trafficSeries)
		trafficRead.GET("/rankings", h.trafficRankings)
		trafficRead.GET("/summary", h.trafficSummary)
		trafficRead.GET("/active-users", h.trafficActiveUsers)
		trafficRead.GET("/protocol-shares", h.trafficProtocolShares)
	}
	if h.Upgrade != nil {
		systemUpgrade := authenticated.Group("/system/upgrade")
		systemUpgrade.GET("/status", h.getUpgradeStatus)
		systemUpgrade.POST("/check", h.checkUpgrade)
		systemUpgrade.POST("/start", h.startUpgrade)
	}
	// 上传恢复挂在 /system/restore（multipart），与 /system/backups/:name
	// 分属不同路由段，避免同段静态/参数路径在 gin 路由树上的冲突。
	if h.Backup != nil && h.Upgrade != nil {
		authenticated.POST("/system/restore", h.restoreUploadedBackup)
	}
	if h.Backup != nil {
		systemBackups := authenticated.Group("/system/backups")
		systemBackups.GET("", h.listBackups)
		systemBackups.POST("", h.createBackup)
		systemBackups.GET("/:name/download", h.downloadBackup)
		systemBackups.POST("/:name/restore", h.restoreExistingBackup)
		systemBackups.DELETE("/:name", h.deleteBackup)
	}
	authenticated.POST("/system/warp/rotate", h.rotateWarp)
}

// registerPublicRoutes 注册无需管理员认证的公开路由。订阅拉取是全项目
// 唯一一条匿名动态路由——新增公开路由前先评估攻击面（限速、脱敏、枚举）。
func (h *handler) registerPublicRoutes(router *gin.Engine) {
	if h.Share == nil {
		return
	}
	router.GET("/subscribe/:token/:format", h.downloadSubscription)
}
