package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gookit/slog"
	"github.com/keepon-online/sb-ops/api"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/database"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	controlegressgeo "github.com/keepon-online/sb-ops/internal/egressgeo"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	"github.com/keepon-online/sb-ops/internal/logging"
	controlmonitor "github.com/keepon-online/sb-ops/internal/monitor"
	controlobservability "github.com/keepon-online/sb-ops/internal/observability"
	controloverview "github.com/keepon-online/sb-ops/internal/overview"
	controlprobe "github.com/keepon-online/sb-ops/internal/probe"
	controlreport "github.com/keepon-online/sb-ops/internal/report"
	argoruntime "github.com/keepon-online/sb-ops/internal/runtime/argo"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicebackup "github.com/keepon-online/sb-ops/internal/service/backup"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	common "github.com/keepon-online/sb-ops/internal/service/common"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	serviceshare "github.com/keepon-online/sb-ops/internal/service/share"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	controlsolusvm "github.com/keepon-online/sb-ops/internal/solusvm"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
	controlwarphealth "github.com/keepon-online/sb-ops/internal/warphealth"
	"github.com/keepon-online/sb-ops/internal/web"
)

type OpsApp struct {
	IdentityStore *identity.Store
	AuditLogs     *controlaudit.Service
	Certificates  *servicecertificate.Service
	Policies      *servicepolicy.Service
	Users         *serviceuser.Service
	Sync          *deployment.SyncService
	Traffic       *controltraffic.Service
	Events        *controlevents.Bus
	Alerts        *controlobservability.Service
	Health        *controlobservability.HealthState
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
	}, "", nil, nil, loggers...)
}

// newOps is the testable constructor: callers pass the apply target, the
// traffic collector knobs, and a config provider for runtime settings. In
// production these come from RuntimeConfig (see New). restoreTrigger registers
// a swap-files action that main runs after the database is closed and before
// the exec restart (backup restore); nil disables restoring.
func newOps(db *sql.DB, sessionTTL time.Duration, apply deployment.ApplyConfig, collector controltraffic.CollectorConfig, configProvider func() config.RuntimeConfig, version string, restart func(), restoreTrigger func(func() error), loggers ...*logging.Logger) (*OpsApp, error) {
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
	// 默认服务器分流护栏：拒绝私网与回环（用户删除后重启会自动补回）。
	if _, err := singBox.EnsurePrivateBlockRule(context.Background()); err != nil {
		logger.Error().Err(err).Msg("初始化默认私网拦截规则失败")
	}
	syncer := deployment.NewSyncService(db, apply, componentLogSink(logger, "sing-box", true))
	argoStore := serviceargo.NewStore(db, secrets)
	argoService := serviceargo.NewService(argoStore, protocolStore)
	// Argo 隧道域名变化（临时隧道重启换址/固定隧道上线）会重新生成订阅产物。
	argoBinaryDir := filepath.Join(filepath.Dir(apply.CacheFilePath), "bin")
	argoManager := argoruntime.NewManager(argoStore, protocolStore, argoBinaryDir, componentLogSink(logger, "argo", false), func(ctx context.Context) {
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
	// 复合 notifier 同时服务告警 outbox 与每日报告直发（报告不经 outbox）。
	notifier := newAlertNotifier(events, configProvider)
	alerts := controlobservability.NewService(db, notifier)
	alerts.SetExpiryWarning(func() time.Duration { return configProvider().Certificates.ExpiryWarning })
	// 提供商带宽计量（SolusVM API，账单口径）：轮询 state 同时喂给告警评估
	// 与总览卡片；未配置 solusvm 段时两侧都不评估（ok=false）。
	solusvmState := controlsolusvm.NewState()
	bandwidthConfigured := func() bool {
		cfg := configProvider().SolusVM
		return cfg.Endpoint != "" && cfg.APIKey != "" && cfg.APIHash != ""
	}
	alerts.SetBandwidthSource(func() (controlobservability.BandwidthStatus, bool) {
		if !bandwidthConfigured() {
			return controlobservability.BandwidthStatus{}, false
		}
		snapshot := solusvmState.Snapshot()
		status := controlobservability.BandwidthStatus{LastSuccess: snapshot.LastSuccess, Failed: snapshot.LastError != ""}
		if snapshot.Sample != nil {
			status.UsedBytes, status.TotalBytes = snapshot.Sample.UsedBytes, snapshot.Sample.TotalBytes
		}
		return status, true
	})
	overview.SetBandwidthSource(func() (controloverview.Bandwidth, bool) {
		if !bandwidthConfigured() {
			return controloverview.Bandwidth{}, false
		}
		snapshot := solusvmState.Snapshot()
		if snapshot.Sample == nil {
			return controloverview.Bandwidth{}, false
		}
		return controloverview.Bandwidth{
			UsedBytes: snapshot.Sample.UsedBytes, TotalBytes: snapshot.Sample.TotalBytes,
			Percent: snapshot.Sample.Percent, UpdatedAt: snapshot.Sample.At,
		}, true
	})
	// Google 出口判定（egressgeo 探测循环）：轮询 state 同时喂给告警评估与
	// 总览卡片；判定翻转自动经 egress_geo_misjudged 告警外发。
	egressGeoState := controlegressgeo.NewState()
	alerts.SetEgressGeoSource(func() (controlobservability.EgressGeoStatus, bool) {
		snapshot := egressGeoState.Snapshot()
		if snapshot.Verdict == "" {
			return controlobservability.EgressGeoStatus{}, false
		}
		return controlobservability.EgressGeoStatus{
			Misjudged: snapshot.Verdict == controlegressgeo.VerdictMisjudged,
			CheckedAt: snapshot.CheckedAt,
		}, true
	})
	overview.SetEgressGeoSource(func() (controloverview.EgressGeo, bool) {
		snapshot := egressGeoState.Snapshot()
		if snapshot.Verdict == "" {
			return controloverview.EgressGeo{}, false
		}
		return controloverview.EgressGeo{Verdict: snapshot.Verdict, CheckedAt: snapshot.CheckedAt, Redirect: snapshot.Redirect}, true
	})
	// WARP MASQUE 解锁链路（warphealth 探测循环）：仅当存在 warp-masque
	// socks 出站才启用（未部署实例静默）；轮询 state 同时喂给告警评估与
	// 总览卡片，degraded 自动经 warp_masque_degraded 告警外发。
	warpHealthState := controlwarphealth.NewState()
	alerts.SetWarpHealthSource(func() (controlobservability.WarpHealthStatus, bool) {
		snapshot := warpHealthState.Snapshot()
		if snapshot.Verdict == "" {
			return controlobservability.WarpHealthStatus{}, false
		}
		return controlobservability.WarpHealthStatus{
			Degraded:  snapshot.Verdict != controlwarphealth.VerdictOK,
			Verdict:   snapshot.Verdict,
			Reason:    snapshot.Reason,
			CheckedAt: snapshot.CheckedAt,
		}, true
	})
	overview.SetWarpHealthSource(func() (controloverview.WarpHealth, bool) {
		snapshot := warpHealthState.Snapshot()
		if snapshot.Verdict == "" {
			return controloverview.WarpHealth{}, false
		}
		return controloverview.WarpHealth{Verdict: snapshot.Verdict, Reason: snapshot.Reason, ExitFamily: snapshot.ExitFamily, CheckedAt: snapshot.CheckedAt}, true
	})
	// 一键换出口：sudo -n 白名单方式执行 warp-cli（只放行 disconnect/connect
	// 两条命令，sudoers 配置见 docs/deployment.md）；未放行时错误归类
	// ErrSudoersMissing 由端点转 409 与配置指引。复测滚动调 Check，终态经
	// state 即时驱动告警与总览。
	warpRotate := func(ctx context.Context) (controlwarphealth.Result, error) {
		addr, ok := warpMasqueProxyAddr(ctx, db)
		if !ok {
			return controlwarphealth.Result{}, controlwarphealth.ErrWarpNotDeployed
		}
		ctx, cancel := context.WithTimeout(ctx, warpRotateTimeout)
		defer cancel()
		return controlwarphealth.Rotate(ctx, sudoWarpCli, addr, warpHealthState)
	}
	// 协议端到端拨测：内核 running 且配置 applied 才放行（内核故障已有
	// runtime_sync_failed 告警，拨测此时写失败行只会制造误报）。
	prober := controlprobe.NewProberWithTargetURLProvider(db, func(ctx context.Context) (bool, string) {
		runtimeProbe := syncer.Probe(ctx)
		if runtimeProbe.ConfigStatus != "applied" {
			return false, runtimeProbe.Reason
		}
		return true, ""
	}, func() string { return configProvider().Probe.TargetURL })
	// 升级源每次操作现取，跟随 config.yaml updates 段热更新。
	upgradeService := controlupgrade.NewService(func() (string, string) {
		cfg := configProvider()
		return cfg.Updates.Repo, cfg.Updates.Token
	}, version, restart, events)
	// 数据备份：dir 闭包现取 data_dir（服务不 import internal/config，照
	// upgrade 的 Source 闭包先例）；dbPath 闭包同理取 server.db_path——换库
	// 文件动作由它定位主库；restoreTrigger 把动作注册进 main 停机链的
	// 「db.Close 之后、exec 之前」窗口。
	backupService := servicebackup.NewService(db, func() string { return configProvider().Server.DataDir }, func() string { return configProvider().Server.DBPath }, restoreTrigger)
	// 定时备份快照（每日 04:00 一轮）同时喂给 backup_failed 告警评估。
	// ScheduleState 纯内存，重启清零：attempted=false（重启后、首轮之前）
	// 时按磁盘事实兜底——最新快照超过 25h 未更新说明备份链断裂（重启前
	// 失败未恢复或停机跨过 04:00 漏跑），照常产生候选防告警被洗白；全新
	// 部署（无快照）与快照尚新不评估。
	backupSchedule := servicebackup.NewScheduleState()
	alerts.SetBackupSource(func() (controlobservability.BackupStatus, bool) {
		lastSuccess, lastError, attempted := backupSchedule.Snapshot()
		if !attempted {
			if newest, stale := backupService.StaleSnapshot(); stale {
				return controlobservability.BackupStatus{
					LastSuccess: newest,
					LastError:   fmt.Sprintf("最新备份快照 %s（UTC）已超过 %d 小时未更新", newest.Format("2006-01-02 15:04:05"), int(servicebackup.SnapshotStaleAfter.Hours())),
				}, true
			}
			return controlobservability.BackupStatus{}, false
		}
		return controlobservability.BackupStatus{LastSuccess: lastSuccess, LastError: lastError}, true
	})
	// 新版本自动发现：升级服务缓存最近一次检查结果（页面展示同一份缓存），
	// 有可升级版本时经 upgrade_available 告警外发。
	alerts.SetUpgradeSource(func() (controlobservability.UpgradeStatus, bool) {
		snapshot := upgradeService.Status()
		if !snapshot.Configured || snapshot.Latest == nil {
			return controlobservability.UpgradeStatus{}, false
		}
		return controlobservability.UpgradeStatus{
			CurrentVersion: snapshot.CurrentVersion,
			LatestVersion:  snapshot.Latest.Version,
			Available:      snapshot.Latest.Upgradable,
		}, true
	})
	health := controlobservability.NewHealthState(nil)
	monitor := controlmonitor.NewService()
	// 每日运行报告的数据源闭包：全部现取，报告组装在 internal/report（纯逻辑）。
	reportSources := controlreport.Sources{
		YesterdayTraffic: func(ctx context.Context) (int64, int64, error) {
			now := time.Now()
			todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			summary, err := traffic.Summary(ctx, controltraffic.Filter{From: todayStart.AddDate(0, 0, -1), To: todayStart, Interval: "hour"})
			if err != nil {
				return 0, 0, err
			}
			return summary.UpBytes, summary.DownBytes, nil
		},
		ActiveUsers: func(ctx context.Context) (int64, error) {
			now := time.Now()
			todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			result, err := traffic.ActiveUsers(ctx, controltraffic.Filter{From: todayStart.AddDate(0, 0, -1), To: todayStart, Interval: "day"})
			if err != nil {
				return 0, err
			}
			return result.TotalUsers, nil
		},
		AlertStats: func(ctx context.Context) (int, int, int, error) {
			stats, err := alerts.Stats(ctx, 2)
			if err != nil {
				return 0, 0, 0, err
			}
			now := time.Now()
			yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1).Format("2006-01-02")
			opened, resolved := 0, 0
			for _, daily := range stats.Daily {
				if daily.Date == yesterday {
					opened, resolved = daily.Opened, daily.Resolved
				}
			}
			return stats.CurrentOpen, opened, resolved, nil
		},
		LatestBackup: func() (int64, int, error) {
			values, err := backupService.List()
			if err != nil || len(values) == 0 {
				return 0, len(values), err
			}
			return values[0].CreatedAt, len(values), nil
		},
		BackupScheduleError: func() string {
			_, lastError, _ := backupSchedule.Snapshot()
			return lastError
		},
		Bandwidth: func() (int64, int64, float64, bool) {
			sample := solusvmState.Snapshot().Sample
			if sample == nil {
				return 0, 0, 0, false
			}
			return sample.UsedBytes, sample.TotalBytes, sample.Percent, true
		},
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(httpx.RecoveryMiddleware(), httpx.RequestIDMiddleware(), securityHeaders())
	web.RegisterHealthRoutes(router, db, health, database.CurrentSchemaVersion)
	api.RegisterRoutes(router, api.Dependencies{
		Identity: auth, Store: identityStore, Users: users, Protocols: protocols, Certificates: certificates, Policies: policies, SingBox: singBox, Sync: syncer,
		Overview: overview, AuditLogs: auditLogs, Share: subscriptions, Traffic: traffic, Events: events, Alerts: alerts, Config: configProvider,
		Argo: argoService, ArgoManager: argoManager, Monitor: monitor, Upgrade: upgradeService, Prober: prober,
		Backup: backupService, WarpRotate: warpRotate,
	})
	background, cancel := context.WithCancel(context.Background())
	value := &OpsApp{
		IdentityStore: identityStore, AuditLogs: auditLogs, Certificates: certificates, Policies: policies, Users: users, Sync: syncer,
		Traffic: traffic, Events: events, Alerts: alerts,
		Router: router, db: db, cancel: cancel, Health: health, logger: logger, config: configProvider, argo: argoManager,
		activeConnections: kernelConnections(syncer),
	}
	if err := syncer.Start(context.Background()); err != nil {
		logger.Error().Err(err).Msg("恢复内嵌 sing-box 运行时失败")
	}
	argoManager.Start()
	value.goLoop(background, "operations", value.runOperationsBackground)
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
	value.goLoop(background, "traffic-collector", func(ctx context.Context) {
		_ = controltraffic.NewLocalCollector(traffic, collector).Run(ctx)
	})
	// 服务器监控指标（CPU/内存/磁盘/网络）为纯内存采样，独立于数据库与
	// sing-box 运行时，失败只影响面板展示。
	value.goLoop(background, "monitor", func(ctx context.Context) {
		_ = monitor.Run(ctx)
	})
	// 设备平台画像：按实时连接的目标域名（微软更新/NCSI、苹果软件更新、
	// FCM 推送等高置信信号）推断 UA 未携带系统信息的设备平台，只回填
	// unknown，不覆盖 UA 识别结果。
	value.goLoop(background, "device-profiler", func(ctx context.Context) {
		_ = newDeviceProfiler(syncer, users, logger).Run(ctx)
	})
	// 协议端到端拨测：启动后等首轮同步落定再首测，之后固定周期滚动。
	value.goLoop(background, "protocol-probe", func(ctx context.Context) {
		runProtocolProbeLoop(ctx, prober, logger, configProvider)
	})
	// VPS 月流量计量：启动即首轮，之后小时级轮询；未配置即空转。
	value.goLoop(background, "solusvm-metering", func(ctx context.Context) {
		runSolusVMMetering(ctx, solusvmState, configProvider, logger)
	})
	// Google 出口判定：启动即首轮，之后固定周期轮询；翻转走
	// egress_geo_misjudged 告警（warning，翻回正常自动恢复）。
	value.goLoop(background, "egress-geo", func(ctx context.Context) {
		runEgressGeoLoop(ctx, egressGeoState, logger)
	})
	// WARP 解锁链路健康：启动延迟 1 分钟避开 warp-svc 竞速，之后 15 分钟
	// 一轮；无 warp-masque 出站时空转，degraded 走 warp_masque_degraded 告警
	// （warning，翻回正常自动恢复）。
	value.goLoop(background, "warp-health", func(ctx context.Context) {
		runWarpHealthLoop(ctx, warpHealthState, db, logger)
	})
	// 面板新版本自动发现：周期检查 GitHub Release，结果进升级服务缓存与
	// upgrade_available 告警。
	value.goLoop(background, "upgrade-check", func(ctx context.Context) {
		runUpgradeCheckLoop(ctx, upgradeService, logger)
	})
	// 定时数据库备份：每日 04:00（本地时区）复用一键备份路径生成快照并裁剪，
	// 失败经 backup_failed 告警外发。
	value.goLoop(background, "backup-schedule", func(ctx context.Context) {
		runBackupScheduleLoop(ctx, backupService, backupSchedule, logger)
	})
	// 通知外发独立于运维检查：外部渠道最坏 10s 超时不应阻塞聚合、告警评估与 readiness。
	value.goLoop(background, "notification-dispatch", value.runNotificationDispatchLoop)
	// 每日运行报告：到点直发复合 notifier（不走 outbox——alert_id 外键要求
	// 真实告警行），失败自带轻量重试。
	value.goLoop(background, "daily-report", func(ctx context.Context) {
		runReportLoop(ctx, configProvider, notifier, reportSources, logger)
	})
	return value, nil
}

// loopRestartBackoff 是循环 panic 退出后的重启退避：防紧密 panic 循环刷爆
// 日志，同时保证监控类循环尽快恢复。
const loopRestartBackoff = 5 * time.Second

// goLoop 在受 wg 管理的 goroutine 里运行一个后台循环。循环体 panic 只重启
// 该循环并记错误日志（含堆栈），不把整个面板带崩——内核与面板同进程，probe
// 等循环直接调第三方 sing-box / gRPC 代码，上游 panic 不应中断全体代理。
// 循环正常返回视为退出（各循环仅在 ctx 取消时返回）；此时同样带退避重启，
// 循环自身 bug 导致的提前退出不至于静默死亡。
func (a *OpsApp) goLoop(ctx context.Context, name string, loop func(context.Context)) {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						a.logger.Error().Str("loop", name).Msg(fmt.Sprintf("后台循环 panic（将自动重启）: %v\n%s", recovered, debug.Stack()))
					}
				}()
				loop(ctx)
			}()
			select {
			case <-ctx.Done():
				return
			case <-time.After(loopRestartBackoff):
			}
		}
	}()
}

// runProtocolProbeLoop 周期拨测循环。内核未就绪的跳过属常态（同步窗口、
// 停机），打 debug 不打扰；其余失败（如读库失败）打错误日志。
func runProtocolProbeLoop(ctx context.Context, prober *controlprobe.Prober, logger *logging.Logger, configProvider func() config.RuntimeConfig) {
	timer := time.NewTimer(controlprobe.ProbeInitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := prober.Probe(ctx, ""); err != nil {
			if errors.Is(err, controlprobe.ErrKernelNotReady) {
				logger.Info().Err(err).Msg("协议拨测跳过")
			} else {
				logger.Error().Err(err).Msg("协议拨测轮失败")
			}
		}
		interval := configProvider().Probe.Interval
		if interval <= 0 {
			interval = controlprobe.ProbeInterval
		}
		timer.Reset(interval)
	}
}

// runSolusVMMetering 按小时轮询提供商的 SolusVM 客户端 API，把账单口径的
// 月流量采样写进共享 state（总览卡片与告警评估的输入）。凭据每轮从
// configProvider 现取（config.yaml 改动热生效）；单次失败只记状态与日志，
// 持续失败由 vps_bandwidth_stalled 告警兜底，不打断轮询节奏。
func runSolusVMMetering(ctx context.Context, state *controlsolusvm.State, configProvider func() config.RuntimeConfig, logger *logging.Logger) {
	timer := time.NewTimer(0) // 启动即首轮，避免重启后长时间无数据。
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if cfg := configProvider().SolusVM; cfg.Endpoint != "" && cfg.APIKey != "" && cfg.APIHash != "" {
			sample, err := controlsolusvm.NewClient(cfg.Endpoint, cfg.APIKey, cfg.APIHash).Info(ctx)
			if err != nil {
				state.RecordError(err)
				logger.Error().Err(err).Msg("SolusVM 流量计量拉取失败")
			} else {
				// 成功不打日志（小时级节奏下纯属噪音），最新采样面板可见。
				state.Record(sample)
			}
		}
		timer.Reset(controlsolusvm.PollInterval)
	}
}

// runEgressGeoLoop 周期探测 Google 对出口 IP 的地区判定（不跟随重定向的
// GET，跳转 google.com.hk 即判 CN 误判）。单次失败只记状态与日志、粘滞上
// 一个已知判定（防网络抖动误 resolve 告警），不打断节奏；成功不打日志。
func runEgressGeoLoop(ctx context.Context, state *controlegressgeo.State, logger *logging.Logger) {
	client := controlegressgeo.NewClient()
	timer := time.NewTimer(0) // 启动即首轮，重启后状态尽快恢复。
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		verdict, redirect, err := controlegressgeo.Check(ctx, client, controlegressgeo.CheckURL)
		if err != nil {
			state.RecordError(err)
			logger.Error().Err(err).Msg("Google 出口判定探测失败")
		} else {
			state.Record(verdict, redirect)
		}
		timer.Reset(controlegressgeo.CheckInterval)
	}
}

// runWarpHealthLoop 周期探测 WARP MASQUE 解锁链路（经 warp-masque socks
// 出站指向的本地 WARP 代理校验 warp=on 与 Google 出口判定）。歧义失败只记
// 状态与日志、粘滞上一个已知判定（防抖）；门控关闭（出站不存在或被删）时
// Reset 回未检测态并空转。成功不打日志。
func runWarpHealthLoop(ctx context.Context, state *controlwarphealth.State, database *sql.DB, logger *logging.Logger) {
	timer := time.NewTimer(controlwarphealth.CheckInitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		proxyAddr, ok := warpMasqueProxyAddr(ctx, database)
		if !ok {
			state.Reset()
			timer.Reset(controlwarphealth.CheckInterval)
			continue
		}
		result, err := controlwarphealth.Check(ctx, proxyAddr)
		if err != nil {
			state.RecordUnknown(err)
			logger.Error().Err(err).Msg("WARP 解锁链路探测失败")
		} else {
			state.Record(result)
		}
		timer.Reset(controlwarphealth.CheckInterval)
	}
}

// warpMasqueProxyAddr 返回 warp-masque socks 出站指向的本地代理地址；出站
// 不存在或类型/取值不符返回 ok=false（用户级门控：未部署链路的实例静默）。
// 直查 singbox_outbounds 与告警评估直查 runtime_state 同例，不引 service 层。
func warpMasqueProxyAddr(ctx context.Context, database *sql.DB) (string, bool) {
	var server string
	var port int
	err := database.QueryRowContext(ctx,
		`SELECT server, server_port FROM singbox_outbounds WHERE tag='warp-masque' AND type='socks' LIMIT 1`).Scan(&server, &port)
	if err != nil || strings.TrimSpace(server) == "" || port <= 0 || port > 65535 {
		return "", false
	}
	return net.JoinHostPort(strings.TrimSpace(server), strconv.Itoa(port)), true
}

// warp-cli 的调用形态：官方客户端装在 /usr/bin/warp-cli，面板运行用户非
// root，经 sudo -n 白名单执行（sudoers 只放行 disconnect/connect 两条命令，
// 配置见 docs/deployment.md）。
const (
	warpCliPath        = "/usr/bin/warp-cli"
	warpCliStepTimeout = 12 * time.Second
	warpRotateTimeout  = 2 * time.Minute
)

// sudoWarpCli 执行一条 warp-cli 子命令；sudoers 未放行时（sudo -n 拒绝）
// 归类 ErrSudoersMissing 供端点转 409 与配置指引。
func sudoWarpCli(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, warpCliStepTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sudo", append([]string{"-n", warpCliPath}, args...)...).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if strings.Contains(text, "a password is required") || strings.Contains(text, "not allowed") {
			return fmt.Errorf("%w: %s", controlwarphealth.ErrSudoersMissing, text)
		}
		return fmt.Errorf("warp-cli %s 失败: %s", strings.Join(args, " "), text)
	}
	return nil
}

// upgradeCheckInitialDelay / upgradeCheckInterval 是新版本自动发现的节奏：
// 启动先等升级与同步落定再首查，之后每 6 小时一查（GitHub 匿名限额 60 次/时，
// 个人面板远够用）。
const (
	upgradeCheckInitialDelay = 2 * time.Minute
	upgradeCheckInterval     = 6 * time.Hour
)

// runBackupScheduleLoop 每日 04:00（本地时区，钟点常量在 backup.ScheduleHour）
// 复用一键备份的服务路径（VACUUM INTO 快照 + 保留裁剪）。成功不打日志（备份
// 列表面板自可见），失败打错误日志并经 backup_failed 告警外发、下轮成功自动
// 恢复；失败不重试，最迟次日同一钟点重试——告警保证静默失败可见。与手动备份
// 同秒撞车（ErrDuplicate）按成功对待：那一刻库快照刚刚落盘。
func runBackupScheduleLoop(ctx context.Context, backups *servicebackup.Service, state *servicebackup.ScheduleState, logger *logging.Logger) {
	timer := time.NewTimer(time.Until(servicebackup.NextRun(time.Now())))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := backups.Create(ctx); err != nil {
			if errors.Is(err, common.ErrDuplicate) {
				state.RecordSuccess(time.Now())
			} else {
				state.RecordFailure(err)
				logger.Error().Err(err).Msg("定时数据库备份失败")
			}
		} else {
			state.RecordSuccess(time.Now())
		}
		timer.Reset(time.Until(servicebackup.NextRun(time.Now())))
	}
}

// runUpgradeCheckLoop 周期检查 GitHub 最新 Release，把结果写进升级服务缓存
// （系统页展示同一份，页面不再需要手动点检查）；有可升级版本时
// upgrade_available 告警经 Evaluate 外发。未配置升级源/升级进行中静默让位，
// 单次失败只记日志——页面手动检查仍可用，不构成需要告警的故障。
func runUpgradeCheckLoop(ctx context.Context, upgrades *controlupgrade.Service, logger *logging.Logger) {
	timer := time.NewTimer(upgradeCheckInitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if _, err := upgrades.Check(ctx); err != nil &&
			!errors.Is(err, controlupgrade.ErrNotConfigured) && !errors.Is(err, controlupgrade.ErrBusy) {
			logger.Error().Err(err).Msg("面板升级自动检查失败")
		}
		timer.Reset(upgradeCheckInterval)
	}
}

// 每日报告的节奏与重试：未启用时每小时重查配置（热开启最多延迟 1 小时
// 拾取，且开启后不会立即补发，只等下一个钟点）；发送失败重试 3 次、间隔
// 1 分钟，全败只记日志——次日自然补上（报告不是告警，不值得更重的机制）。
const (
	reportConfigPollInterval = time.Hour
	reportSendAttempts       = 3
	reportSendRetryInterval  = time.Minute
)

// runReportLoop 到点组装上一自然日报告并直调复合 notifier（不走 outbox——
// notification_outbox.alert_id 外键要求真实告警行）。Notification.ID 由报告
// 日派生，事件总线按幂等键去重，面板侧不会重复；NextRun 前瞻保证重启后
// 已过钟点不重发。armed 标记区分「配置轮询唤醒」与「到点唤醒」：从禁用
// 切到启用只布防下一个钟点，不立即补发。
func runReportLoop(ctx context.Context, configProvider func() config.RuntimeConfig, notifier controlobservability.Notifier, sources controlreport.Sources, logger *logging.Logger) {
	initial := configProvider().Report
	armed := initial.Enabled
	var delay time.Duration
	if armed {
		delay = time.Until(controlreport.NextRun(time.Now(), initial.Hour))
	} else {
		delay = reportConfigPollInterval
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		reportConfig := configProvider().Report
		if !reportConfig.Enabled {
			armed = false
			timer.Reset(reportConfigPollInterval)
			continue
		}
		if !armed {
			armed = true
			timer.Reset(time.Until(controlreport.NextRun(time.Now(), reportConfig.Hour)))
			continue
		}
		sendDailyReport(ctx, notifier, sources, logger)
		timer.Reset(time.Until(controlreport.NextRun(time.Now(), reportConfig.Hour)))
	}
}

func sendDailyReport(ctx context.Context, notifier controlobservability.Notifier, sources controlreport.Sources, logger *logging.Logger) {
	daily := controlreport.BuildDaily(ctx, time.Now(), sources)
	payload, err := json.Marshal(map[string]string{"title": daily.Title, "message": daily.Message})
	if err != nil {
		logger.Error().Err(err).Msg("每日运行报告组装失败")
		return
	}
	notification := controlobservability.Notification{ID: daily.EventID, EventType: "report.daily", Payload: payload}
	for attempt := 1; attempt <= reportSendAttempts; attempt++ {
		if err := notifier.Notify(ctx, notification); err == nil {
			return
		} else if attempt == reportSendAttempts {
			logger.Error().Err(err).Msg("每日运行报告推送失败")
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(reportSendRetryInterval):
			}
		}
	}
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
		controlobservability.NewDomesticNotifier(func(context.Context) (controlobservability.DomesticConfig, error) {
			cfg := configProvider()
			return controlobservability.DomesticConfig{
				FeishuWebhookURL:   cfg.Notifications.FeishuWebhookURL,
				FeishuSecret:       cfg.Notifications.FeishuSecret,
				DingTalkWebhookURL: cfg.Notifications.DingTalkWebhookURL,
				DingTalkSecret:     cfg.Notifications.DingTalkSecret,
				WeComWebhookURL:    cfg.Notifications.WeComWebhookURL,
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
	// 流量日桶按服务器本地时区切天，启动时全量重建一次：日界偏移变化（时区
	// 调整、版本升级）后存量桶原地修正；个人规模毫秒级，失败不影响其余任务。
	if err := a.Traffic.RebuildDaily(ctx); err != nil {
		a.logger.Error().Err(err).Msg("流量日桶全量重建失败")
	}
	a.runOperationalChecks(ctx)
	a.runHousekeeping(ctx)
	a.renewCertificates(ctx)
	operationsTicker := time.NewTicker(time.Minute)
	retentionTicker := time.NewTicker(6 * time.Hour)
	certificateTicker := time.NewTicker(6 * time.Hour)
	defer operationsTicker.Stop()
	defer retentionTicker.Stop()
	defer certificateTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-operationsTicker.C:
			a.runOperationalChecks(ctx)
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
		// RenewDueACME 单张失败不阻断其余，此时 renewed 可能 > 0：
		// 记错误后继续走重同步，别让已续期的证书滞留在旧内核配置里。
		a.logger.Error().Err(err).Msg("ACME 证书续期失败")
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
	a.runAuditRetention(ctx)
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
func (a *OpsApp) runAuditRetention(ctx context.Context) {
	if a.AuditLogs == nil {
		return
	}
	days := a.config().Audit.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	removed, err := a.AuditLogs.PurgeBefore(ctx, cutoff)
	if err != nil {
		a.logger.Error().Err(err).Msg("审计日志留存清理失败")
		return
	}
	if removed > 0 {
		a.logger.Info().Int64("removed", removed).Msg("审计日志留存清理完成")
	}
}

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
	a.resetDueQuotas(ctx)
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

// resetDueQuotas 消费用户的周期额度重置：next_reset_at 到期的用户用量清零并
// 推进边界，逐户落系统审计（无登录 actor）；有用户因此从额度超额恢复可用时
// 重新同步内核——超额剔除只发生在编译期，不触发同步凭据就回不了协议入站。
// 放在 verifyKernelHealth 之后：重置引发的同步刚重启过内核，紧接着的健康
// 探测容易撞上启动窗口再触发一次冗余同步。
func (a *OpsApp) resetDueQuotas(ctx context.Context) {
	if a.Users == nil {
		return
	}
	result, err := a.Users.ResetDueQuotas(ctx)
	if err != nil {
		a.logger.Error().Err(err).Msg("周期额度重置失败")
		return
	}
	if len(result.Reset) == 0 {
		return
	}
	a.logger.Info().Int("reset", len(result.Reset)).Msg("周期额度重置完成")
	// 审计写库剥离取消：停机瞬间进行中的重置也要留下痕。
	auditCtx := context.WithoutCancel(ctx)
	for _, reset := range result.Reset {
		a.appendQuotaResetAudit(auditCtx, reset)
	}
	if len(result.Recovered) > 0 && a.Sync != nil {
		if _, err := a.Sync.Reconcile(ctx); err != nil {
			a.logger.Error().Err(err).Msg("额度恢复用户后重新同步失败")
		}
	}
}

func (a *OpsApp) appendQuotaResetAudit(ctx context.Context, reset serviceuser.QuotaReset) {
	if a.AuditLogs == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"usedBytes": reset.Before.UsedBytes, "nextResetAt": reset.NextResetAt,
	})
	if err != nil {
		payload = []byte("{}")
	}
	if err := a.AuditLogs.Append(ctx, controlaudit.Record{
		ID: uuid.NewString(), Action: api.ActionUserTrafficAutoReset,
		ResourceType: api.ResourceUser, ResourceID: reset.Before.ID, Result: api.ResultSuccess,
		DetailsJSON: string(payload), CreatedAt: time.Now().UTC(),
	}); err != nil {
		// 与 handler 侧同款取舍：审计失败不影响业务结果，但必须留痕。
		a.logger.Error().Err(err).Str("userId", reset.Before.ID).Msg("写入周期额度重置审计失败")
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

func (a *OpsApp) runNotificationDispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	// 启动时先尝试一次，避免重启后等待完整一个周期。
	a.dispatchNotifications(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.dispatchNotifications(ctx)
		}
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

// componentLogSink routes a subprocess's log lines into the panel logger
// under the given component tag. dropDebug controls whether debug lines are
// discarded (kernel, chatty) or downgraded to info (argo).
func componentLogSink(logger *logging.Logger, component string, dropDebug bool) func(level, message string) {
	return func(level, message string) {
		var entry *logging.Entry
		switch level {
		case "warn":
			entry = logger.Warn()
		case "error":
			entry = logger.Error()
		case "debug":
			if dropDebug {
				return
			}
			entry = logger.Info()
		default:
			entry = logger.Info()
		}
		entry.Str("component", component).Msg(message)
	}
}
