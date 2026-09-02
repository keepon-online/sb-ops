package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gookit/slog"
	"github.com/keepon-online/sb-ops/api"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/database"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	"github.com/keepon-online/sb-ops/internal/logging"
	controlmonitor "github.com/keepon-online/sb-ops/internal/monitor"
	controlobservability "github.com/keepon-online/sb-ops/internal/observability"
	controloverview "github.com/keepon-online/sb-ops/internal/overview"
	argoruntime "github.com/keepon-online/sb-ops/internal/runtime/argo"
	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	serviceshare "github.com/keepon-online/sb-ops/internal/service/share"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
	"github.com/keepon-online/sb-ops/internal/web"
)

type OpsApp struct {
	Auth          *identity.Service
	IdentityStore *identity.Store
	Users         *serviceuser.Service
	Protocols     *serviceprotocol.Service
	Certificates  *servicecertificate.Service
	Policies      *servicepolicy.Service
	SingBox       *servicesingbox.Service
	Sync          *deployment.SyncService
	Overview      *controloverview.Service
	AuditLogs     *controlaudit.Service
	Subscriptions *serviceshare.Service
	Traffic       *controltraffic.Service
	Events        *controlevents.Bus
	Alerts        *controlobservability.Service
	Health        *controlobservability.HealthState
	Upgrade       *controlupgrade.Service
	Router        *gin.Engine
	db            *sql.DB
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	logger        *logging.Logger
	config        func() config.RuntimeConfig
	argo          *argoruntime.Manager
	// activeConnections reports how many proxy connections the kernel
	// currently serves; it distinguishes an idle box from a broken collection
	// loop in the stall check. Nil behaves as "no observable connections".
	activeConnections func(ctx context.Context) int
}

// NewOps wires the single-VPS control plane. Resource changes synchronize the
// local sing-box configuration directly, while a local collector polls the box
// for traffic instead of receiving agent reports.
func NewOps(db *sql.DB, sessionTTL time.Duration, loggers ...*logging.Logger) (*OpsApp, error) {
	return newOps(db, sessionTTL, deployment.ApplyConfig{}, controltraffic.CollectorConfig{}, func() config.RuntimeConfig {
		return config.DefaultRuntimeConfig()
	}, "", nil, loggers...)
}

// newOps is the testable constructor: callers pass the apply target, the
// traffic collector knobs, and a config provider for runtime settings. In
// production these come from RuntimeConfig (see New).
func newOps(db *sql.DB, sessionTTL time.Duration, apply deployment.ApplyConfig, collector controltraffic.CollectorConfig, configProvider func() config.RuntimeConfig, version string, restart func(), loggers ...*logging.Logger) (*OpsApp, error) {
	logger := logging.DefaultLogger()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	if err := database.Migrate(context.Background(), db); err != nil {
		return nil, err
	}
	identityStore := identity.NewStore(db)
	auth := identity.NewService(identityStore, sessionTTL)
	userStore := serviceuser.NewStore(db)
	users := serviceuser.NewService(userStore)
	secrets := secret.NewStore(db)
	certificates := servicecertificate.NewService(servicecertificate.NewStore(db), secrets)
	protocolStore := serviceprotocol.NewStore(db, secrets)
	protocols := serviceprotocol.NewService(protocolStore)
	policies := servicepolicy.NewService(servicepolicy.NewStore(db))
	singBox := servicesingbox.NewService(servicesingbox.NewStore(db, secrets))
	if _, err := singBox.EnsureClashAPISecret(context.Background()); err != nil {
		logger.Error().Err(err).Msg("初始化 Clash API 密钥失败")
	}
	if _, err := policies.EnsureDefaultClashPolicy(context.Background()); err != nil {
		return nil, err
	}
	syncer := deployment.NewSyncService(db, apply, kernelLogSink(logger))
	argoStore := serviceargo.NewStore(db, secrets)
	argoService := serviceargo.NewService(argoStore, protocolStore)
	// Argo 隧道域名变化（临时隧道重启换址/固定隧道上线）会重新生成订阅产物。
	argoBinaryDir := filepath.Join(filepath.Dir(apply.CacheFilePath), "bin")
	argoManager := argoruntime.NewManager(argoStore, protocolStore, argoBinaryDir, argoLogSink(logger), func(ctx context.Context) {
		if _, err := syncer.Sync(ctx); err != nil {
			logger.Error().Err(err).Msg("Argo 域名变化后重新同步失败")
		}
	})
	traffic := controltraffic.NewService(db)
	overview := controloverview.NewServiceWithTraffic(db, syncer, traffic)
	auditLogs := controlaudit.NewService(db)
	// 订阅下载按 UA 自动发现设备：新设备创建后立即重新编译同步（与请求
	// 生命周期解耦），失败由订阅服务回退默认设备产物。
	subscriptions := serviceshare.NewService(db, users, func() error {
		_, err := syncer.Sync(context.Background())
		return err
	})
	events := controlevents.NewBus(db)
	alerts := controlobservability.NewService(db, newAlertNotifier(events, configProvider))
	// 升级源每次操作现取，跟随 config.yaml updates 段热更新。
	upgradeService := controlupgrade.NewService(func() (string, string) {
		cfg := configProvider()
		return cfg.Updates.Repo, cfg.Updates.Token
	}, version, restart, events)
	health := controlobservability.NewHealthState(nil)
	monitor := controlmonitor.NewService()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(httpx.RecoveryMiddleware(), httpx.RequestIDMiddleware(), securityHeaders())
	web.RegisterHealthRoutes(router, db, health, database.CurrentSchemaVersion)
	api.RegisterRoutes(router, api.Dependencies{
		Identity: auth, Store: identityStore, Users: users, Protocols: protocols, Certificates: certificates, Policies: policies, SingBox: singBox, Sync: syncer,
		Overview: overview, AuditLogs: auditLogs, Share: subscriptions, Traffic: traffic, Events: events, Alerts: alerts, Config: configProvider,
		Argo: argoService, ArgoManager: argoManager, Monitor: monitor, Upgrade: upgradeService,
	})
	background, cancel := context.WithCancel(context.Background())
	value := &OpsApp{
		Auth: auth, IdentityStore: identityStore, Users: users, Protocols: protocols,
		Certificates: certificates, Policies: policies, SingBox: singBox, Sync: syncer, Overview: overview, AuditLogs: auditLogs, Subscriptions: subscriptions, Traffic: traffic, Events: events, Alerts: alerts,
		Router: router, db: db, cancel: cancel, Health: health, Upgrade: upgradeService, logger: logger, config: configProvider, argo: argoManager,
		activeConnections: kernelConnections(syncer),
	}
	if err := syncer.Start(context.Background()); err != nil {
		logger.Error().Err(err).Msg("恢复内嵌 sing-box 运行时失败")
	}
	argoManager.Start()
	value.wg.Add(1)
	go func() {
		defer value.wg.Done()
		value.runOperationsBackground(background)
	}()
	// Traffic collection uses the panel-managed V2Ray stats gRPC API, which
	// provides per-user attribution. The address is resolved on every cycle so
	// panel changes apply without a restart.
	collector.V2RayResolver = func(ctx context.Context) (string, bool) {
		address, enabled, err := singBox.V2RayAPIEndpoint(ctx)
		if err != nil || !enabled {
			return "", false
		}
		return address, true
	}
	collector.InboundResolver = func(ctx context.Context, tag string) (string, bool) {
		values, err := protocols.List(ctx)
		if err != nil {
			return "", false
		}
		for _, value := range values {
			if value.InboundTag() == tag {
				return value.ID, true
			}
		}
		return "", false
	}
	// 入站用户 name 是设备 id：每个采集周期拉一次 deviceID→userID 归因表。
	collector.DeviceResolver = func(ctx context.Context) (map[string]string, error) {
		return userStore.DeviceUserMap(ctx)
	}
	collector.OnError = func(err error) {
		logger.Error().Err(err).Msg("本地流量采集失败")
	}
	value.wg.Add(1)
	go func() {
		defer value.wg.Done()
		_ = controltraffic.NewLocalCollector(traffic, collector).Run(background)
	}()
	// 服务器监控指标（CPU/内存/磁盘/网络）为纯内存采样，独立于数据库与
	// sing-box 运行时，失败只影响面板展示。
	value.wg.Add(1)
	go func() {
		defer value.wg.Done()
		_ = monitor.Run(background)
	}()
	// 设备平台画像：按实时连接的目标域名（微软更新/NCSI、苹果软件更新、
	// FCM 推送等高置信信号）推断 UA 未携带系统信息的设备平台，只回填
	// unknown，不覆盖 UA 识别结果。
	value.wg.Add(1)
	go func() {
		defer value.wg.Done()
		_ = newDeviceProfiler(syncer, users, logger).Run(background)
	}()
	return value, nil
}

// deviceProfileStoreAdapter bridges the user service onto the profiler store
// interface; the local ref type keeps observability free of service imports.
type deviceProfileStoreAdapter struct {
	users *serviceuser.Service
}

func (a deviceProfileStoreAdapter) UnknownPlatformDevices(ctx context.Context) ([]controlobservability.UnknownDeviceRef, error) {
	refs, err := a.users.UnknownPlatformDevices(ctx)
	if err != nil {
		return nil, err
	}
	values := make([]controlobservability.UnknownDeviceRef, 0, len(refs))
	for _, ref := range refs {
		values = append(values, controlobservability.UnknownDeviceRef{UserID: ref.UserID, DeviceID: ref.DeviceID, SourceIP: ref.SourceIP})
	}
	return values, nil
}

func (a deviceProfileStoreAdapter) UpdateDevicePlatform(ctx context.Context, userID, deviceID, platform string) error {
	return a.users.UpdateDevicePlatform(ctx, userID, deviceID, platform)
}

func newDeviceProfiler(syncer *deployment.SyncService, users *serviceuser.Service, logger *logging.Logger) *controlobservability.DeviceProfiler {
	return &controlobservability.DeviceProfiler{
		Snapshot: func(ctx context.Context) ([]controlobservability.ConnectionSample, error) {
			result, err := syncer.Connections(ctx)
			if err != nil {
				return nil, err
			}
			samples := make([]controlobservability.ConnectionSample, 0, len(result.Items))
			for _, item := range result.Items {
				samples = append(samples, controlobservability.ConnectionSample{SourceIP: item.SourceIP, Host: item.Host})
			}
			return samples, nil
		},
		Store: deviceProfileStoreAdapter{users: users},
		Log: func(level slog.Level, message string) {
			if level >= slog.WarnLevel {
				logger.Warn().Msg(message)
			} else {
				logger.Info().Msg(message)
			}
		},
	}
}

// newAlertNotifier wires alert delivery: the embedded event bus always carries
// alerts to open panels, and an optional webhook/Telegram target is resolved
// from the current runtime config on every dispatch so configuration changes
// take effect without a restart.
func newAlertNotifier(events *controlevents.Bus, configProvider func() config.RuntimeConfig) controlobservability.Notifier {
	return controlobservability.NewCompositeNotifier(
		controlobservability.NewEventNotifier(events),
		controlobservability.NewWebhookNotifier(func(context.Context) (controlobservability.WebhookConfig, error) {
			cfg := configProvider()
			return controlobservability.WebhookConfig{
				URL:      cfg.Notifications.WebhookURL,
				Secret:   cfg.Notifications.WebhookSecret,
				BotToken: cfg.Notifications.TelegramBotToken,
				ChatID:   cfg.Notifications.TelegramChatID,
			}, nil
		}),
	)
}

// securityHeaders sets baseline response-hardening headers. The CSP allows
// antd's runtime-injected inline styles but no inline scripts and no
// cross-origin connects.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "same-origin")
		// fonts.googleapis/gstatic 是内嵌 index.html 唯一的跨源引用；
		// 若要收敛到 'self'，需先自托管字体并移除 index.html 的引用。
		c.Header("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		c.Next()
	}
}

func (a *OpsApp) runOperationsBackground(ctx context.Context) {
	a.Health.MarkBackgroundRunning(true)
	defer a.Health.MarkBackgroundRunning(false)
	a.runOperationalChecks(ctx)
	a.dispatchNotifications(ctx)
	a.runHousekeeping(ctx)
	a.renewCertificates(ctx)
	operationsTicker := time.NewTicker(time.Minute)
	dispatchTicker := time.NewTicker(5 * time.Second)
	retentionTicker := time.NewTicker(6 * time.Hour)
	certificateTicker := time.NewTicker(6 * time.Hour)
	defer operationsTicker.Stop()
	defer dispatchTicker.Stop()
	defer retentionTicker.Stop()
	defer certificateTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-operationsTicker.C:
			a.runOperationalChecks(ctx)
		case <-dispatchTicker.C:
			a.dispatchNotifications(ctx)
		case <-retentionTicker.C:
			a.runHousekeeping(ctx)
		case <-certificateTicker.C:
			a.renewCertificates(ctx)
		}
	}
}

// renewCertificates renews ACME certificates approaching expiry and re-syncs the
// embedded core so the refreshed material is applied.
func (a *OpsApp) renewCertificates(ctx context.Context) {
	if a.Certificates == nil {
		return
	}
	cfg := a.config()
	renewed, err := a.Certificates.RenewDueACME(ctx, servicecertificate.AcmeOptions{
		Email: cfg.Acme.Email, Provider: cfg.Acme.DNSProvider, APIToken: cfg.Acme.APIToken,
	}, 30*24*time.Hour)
	if err != nil {
		a.logger.Error().Err(err).Msg("ACME 证书续期失败")
		return
	}
	if renewed == 0 {
		return
	}
	a.logger.Info().Int("renewed", renewed).Msg("ACME 证书续期完成")
	if a.Sync != nil {
		if _, err := a.Sync.Reconcile(ctx); err != nil {
			a.logger.Error().Err(err).Msg("证书续期后重新同步失败")
		}
	}
}

// notificationRetention is how long finished (delivered or terminally failed)
// notifications stay in the outbox for inspection before housekeeping purges
// them.
const notificationRetention = 7 * 24 * time.Hour

// runHousekeeping ages out data that no longer needs to stay queryable: traffic
// history past its retention window, expired or revoked sessions, and finished
// notifications older than the notification retention window.
func (a *OpsApp) runHousekeeping(ctx context.Context) {
	a.runTrafficRetention(ctx)
	if a.IdentityStore != nil {
		removed, err := a.IdentityStore.DeleteStaleSessions(ctx, time.Now().UTC())
		if err != nil {
			a.logger.Error().Err(err).Msg("过期会话清理失败")
		} else if removed > 0 {
			a.logger.Info().Int64("removed", removed).Msg("过期会话清理完成")
		}
	}
	if a.Alerts != nil {
		removed, err := a.Alerts.PurgeNotifications(ctx, time.Now().UTC().Add(-notificationRetention))
		if err != nil {
			a.logger.Error().Err(err).Msg("通知外发记录清理失败")
		} else if removed > 0 {
			a.logger.Info().Int64("removed", removed).Msg("通知外发记录清理完成")
		}
	}
}

// runTrafficRetention ages out traffic history and raw ingestion batches older
// than the configured retention window (traffic.retention_days; 0 or unset keeps
// everything forever).
func (a *OpsApp) runTrafficRetention(ctx context.Context) {
	if a.Traffic == nil {
		return
	}
	days := a.config().Traffic.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	removed, err := a.Traffic.PurgeBefore(ctx, cutoff)
	if err != nil {
		a.logger.Error().Err(err).Msg("流量留存清理失败")
		return
	}
	if removed > 0 {
		a.logger.Info().Int64("removed", removed).Str("cutoff", cutoff.Format(time.RFC3339)).Msg("流量留存清理完成")
	}
}

func (a *OpsApp) runOperationalChecks(ctx context.Context) {
	aggregationErr := a.Traffic.AggregateRecent(ctx)
	a.Health.RecordAggregation(aggregationErr)
	if aggregationErr != nil {
		a.logger.Error().Err(aggregationErr).Msg("流量聚合失败")
	}
	stallErr := a.trafficStallError(ctx)
	a.Health.RecordTrafficCollection(stallErr)
	if stallErr != nil {
		a.logger.Error().Err(stallErr).Msg("流量采集健康检查失败")
	}
	alertErr := a.Alerts.Evaluate(ctx, aggregationErr, stallErr != nil)
	a.Health.RecordAlertEvaluation(alertErr)
	if alertErr != nil {
		a.logger.Error().Err(alertErr).Msg("告警评估失败")
	}
	if err := a.Events.Retention(ctx); err != nil {
		a.logger.Error().Err(err).Msg("系统事件保留清理失败")
	}
	a.verifyKernelHealth(ctx)
}

// verifyKernelHealth dials the embedded kernel through the Clash API and
// reconciles when a supposedly running kernel stops responding, so a silently
// dead core recovers within one operational cycle.
func (a *OpsApp) verifyKernelHealth(ctx context.Context) {
	if a.Sync == nil {
		return
	}
	verified, err := a.Sync.VerifyKernel(ctx)
	if !verified || err == nil {
		return
	}
	a.logger.Error().Err(err).Msg("内核健康探测失败，尝试自动重新同步")
	if _, syncErr := a.Sync.Reconcile(ctx); syncErr != nil {
		a.logger.Error().Err(syncErr).Msg("内核自愈重新同步失败")
	}
}

// trafficStallThreshold is how long traffic collection may go without a
// successful cycle before it is reported as stalled.
const trafficStallThreshold = 5 * time.Minute

func (a *OpsApp) trafficStallError(ctx context.Context) error {
	if a.Traffic == nil {
		return nil
	}
	initialized, err := a.IdentityStore.Initialized(ctx)
	if err != nil || !initialized {
		// Collection has no expectation to run before setup completes.
		return nil
	}
	if !a.Traffic.CollectionStalled(time.Now().UTC(), trafficStallThreshold) {
		return nil
	}
	if a.observableConnections(ctx) == 0 {
		// No observable proxy connections means an idle box: a healthy
		// collection loop produces no samples either, so a missing heartbeat
		// alone must not degrade logs, alerts, or readiness.
		return nil
	}
	return errors.New("流量采集循环超过阈值未成功完成（存在活跃连接但无采集心跳，V2Ray stats 查询或入库持续失败）")
}

// observableConnections returns the number of active proxy connections observed
// through the Clash API. An unavailable API also reports zero: without
// observable connections the box is treated as idle, and genuine collection
// failures still surface through the per-cycle「本地流量采集失败」log.
func (a *OpsApp) observableConnections(ctx context.Context) int {
	if a.activeConnections == nil {
		return 0
	}
	return a.activeConnections(ctx)
}

// kernelConnections adapts the deployment sync service into the stall-check
// connection probe.
func kernelConnections(sync *deployment.SyncService) func(context.Context) int {
	return func(ctx context.Context) int {
		if sync == nil {
			return 0
		}
		connections, err := sync.Connections(ctx)
		if err != nil || !connections.Available {
			return 0
		}
		return len(connections.Items)
	}
}

func (a *OpsApp) dispatchNotifications(ctx context.Context) {
	if err := a.Alerts.Dispatch(ctx); err != nil {
		a.logger.Error().Err(err).Msg("告警通知分发失败")
	}
}

// BeginShutdown stops the SSE event streams and the background loops before
// the HTTP listener drains: http.Server.Shutdown never cancels request
// contexts of active connections, so an open /events stream would otherwise
// keep the unbounded drain waiting forever.
func (a *OpsApp) BeginShutdown() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.Events != nil {
		a.Events.Shutdown()
	}
}

func (a *OpsApp) Close() error {
	if a == nil || a.db == nil {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	// Wait for the operations loop and the traffic collector to actually exit
	// instead of cutting them off on a grace timer: both loops return on
	// context cancellation and carry their own I/O timeouts (gRPC dial,
	// request deadlines), so the wait is bounded by those, not by a constant.
	a.wg.Wait()
	if a.argo != nil {
		a.argo.Close()
	}
	var closeErr error
	if a.Sync != nil {
		closeErr = a.Sync.Close()
	}
	err := a.db.Close()
	a.db = nil
	if closeErr != nil {
		return closeErr
	}
	return err
}

// kernelLogSink routes embedded sing-box log lines into the panel logger so
// kernel output shares the panel's timestamp format, colors, and rotating
// file sink instead of sing-box's own stderr format.
func kernelLogSink(logger *logging.Logger) runtimebox.LogSink {
	return func(level, message string) {
		var entry *logging.Entry
		switch level {
		case "warn":
			entry = logger.Warn()
		case "error":
			entry = logger.Error()
		case "debug":
			return
		default:
			entry = logger.Info()
		}
		entry.Str("component", "sing-box").Msg(message)
	}
}

// argoLogSink routes cloudflared supervisor messages into the panel logger
// under the argo component tag.
func argoLogSink(logger *logging.Logger) argoruntime.LogFunc {
	return func(level, message string) {
		var entry *logging.Entry
		switch level {
		case "warn":
			entry = logger.Warn()
		case "error":
			entry = logger.Error()
		default:
			entry = logger.Info()
		}
		entry.Str("component", "argo").Msg(message)
	}
}
