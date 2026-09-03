package api

// 审计日志取值注册表：action / resourceType / result 的全部合法取值集中在此，
// handler 调用 audit / mutate 时不要再手写字符串字面量。这些值会落库并被前端
// util/labels.ts 映射为中文标签，属于对外契约——只增不改，不能重命名。
//
// action 命名约定 <域>.<操作>（如 protocol.create、singbox.route-rule.order）。

const (
	resultSuccess = "success"
	resultFailure = "failure"
)

// resourceType 取值。
const (
	resourceAccount           = "account"
	resourceSession           = "session"
	resourceAlert             = "alert"
	resourceUser              = "user"
	resourceProtocol          = "protocol"
	resourceCertificate       = "certificate"
	resourcePolicy            = "policy"
	resourceRule              = "rule"
	resourceSingBoxConfig     = "singbox_config"
	resourceSingBoxOutbound   = "singbox_outbound"
	resourceSingBoxRouteRule  = "singbox_route_rule"
	resourceSingBoxRuleSet    = "singbox_rule_set"
	resourceSingBoxRuntime    = "singbox_runtime"
	resourceSingBoxConnection = "singbox_connection"
	resourceArgoTunnel        = "argo_tunnel"
)

// action 取值。
const (
	// 账号与会话
	actionSetupAdminInitialize = "setup.admin.initialize"
	actionAuthLogin            = "auth.login"
	actionAuthPasswordChange   = "auth.password.change"
	actionAuthLogout           = "auth.logout"

	// 告警
	actionAlertAcknowledge = "alert.acknowledge"

	// 内核配置同步（mutate 收尾自动记录）
	actionRuntimeSync = "runtime.sync"

	// sing-box 服务端配置与运行时
	actionSingBoxConnectionClose  = "singbox.connection.close"
	actionSingBoxRuntimeCheck     = "singbox.runtime.check"
	actionSingBoxRuntimeReconcile = "singbox.runtime.reconcile"
	actionSingBoxRuntimeRestart   = "singbox.runtime.restart"
	actionSingBoxConfigUpdate     = "singbox.config.update"
	actionSingBoxOutboundCreate   = "singbox.outbound.create"
	actionSingBoxOutboundUpdate   = "singbox.outbound.update"
	actionSingBoxOutboundDelete   = "singbox.outbound.delete"
	actionSingBoxRouteRuleCreate  = "singbox.route-rule.create"
	actionSingBoxRouteRuleUpdate  = "singbox.route-rule.update"
	actionSingBoxRouteRuleDelete  = "singbox.route-rule.delete"
	actionSingBoxRouteRuleOrder   = "singbox.route-rule.order"
	actionArgoTunnelCreate        = "argo.tunnel.create"
	actionArgoTunnelUpdate        = "argo.tunnel.update"
	actionArgoTunnelDelete        = "argo.tunnel.delete"
	actionArgoTunnelRestart       = "argo.tunnel.restart"
	actionSingBoxRuleSetCreate    = "singbox.rule-set.create"
	actionSingBoxRuleSetUpdate    = "singbox.rule-set.update"
	actionSingBoxRuleSetDelete    = "singbox.rule-set.delete"

	// 协议
	actionProtocolCreate       = "protocol.create"
	actionProtocolUpdate       = "protocol.update"
	actionProtocolDelete       = "protocol.delete"
	actionProtocolClone        = "protocol.clone"
	actionProtocolSecretRotate = "protocol.secret.rotate"

	// 证书
	actionCertificateSelfSignedCreate     = "certificate.self_signed.create"
	actionCertificateSelfSignedRegenerate = "certificate.self_signed.regenerate"
	actionCertificateCreate               = "certificate.create"
	actionCertificateAcmeCreate           = "certificate.acme.create"
	actionCertificateAcmeRenew            = "certificate.acme.renew"
	actionCertificateUpdate               = "certificate.update"
	actionCertificateDelete               = "certificate.delete"

	// 订阅分流（策略与规则）
	actionPolicyCreate = "policy.create"
	actionPolicyUpdate = "policy.update"
	actionPolicyDelete = "policy.delete"
	actionPolicyClone  = "policy.clone"
	actionRuleCreate   = "rule.create"
	actionRuleUpdate   = "rule.update"
	actionRuleDelete   = "rule.delete"
	actionRuleOrder    = "rule.order"

	// 用户
	actionUserCreate        = "user.create"
	actionUserUpdate        = "user.update"
	actionUserDelete        = "user.delete"
	actionUserBatchStatus   = "user.batch-status"
	actionUserTrafficReset  = "user.traffic.reset"
	actionUserTrafficAdjust = "user.traffic.adjust"
	actionUserQuotaAdjust   = "user.quota.adjust"

	// 用户设备
	actionDeviceUpdate = "user.device.update"
	actionDeviceDelete = "user.device.delete"
	actionDeviceRotate = "user.device.rotate"

	actionUserSubscriptionRotate = "user.subscription.rotate"
	resourceDevice               = "user_device"

	// 面板升级
	resourceSystemUpgrade    = "system_upgrade"
	actionSystemUpgradeCheck = "system.upgrade.check"
	actionSystemUpgradeStart = "system.upgrade.start"
)

// 单例资源的固定 resourceID：singbox_config 是 store 层 id=1 的单行表，
// 运行时审计以 "local" 指代本地内嵌内核。
const (
	singBoxConfigID = "1"
	localRuntimeID  = "local"
)
