package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/keepon-online/sb-ops/internal/app"
	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/logging"
)

var version = "dev"

type managedServer struct {
	name   string
	server *http.Server
	serve  func() error
}

type serverResult struct {
	name string
	err  error
}

func main() {
	if err := run(); err != nil {
		slog.Error("服务异常退出", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := config.DefaultConfigPath
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	// 自升级重启：升级服务替换二进制后置标志并向自身发 SIGTERM，走下方
	// 完整的优雅停机链（SSE → HTTP drain → 内核/数据库），收尾后 exec
	// 重生为磁盘上的新二进制，而非让进程退出等待外部拉起。
	var restartRequested atomic.Bool
	restarter := func() {
		restartRequested.Store(true)
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
			slog.Error("发送升级重启信号失败", "error", err)
		}
	}
	// 备份恢复重启：换数据库文件必须等 db.Close（WAL 持有文件句柄），
	// 所以恢复服务把换文件动作注册进此槽，复用 restarter 触发停机链，
	// 停机链在数据库关闭后、exec 重生前执行它。
	var restartAction atomic.Pointer[func() error]
	restoreTrigger := func(action func() error) {
		restartAction.Store(&action)
		restarter()
	}

	cfg, stop, err := config.StartRuntimeConfigWatcher(configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	defer stop()
	current := cfg.Current()
	logger, logCloser, err := logging.New(current.Server.DataDir, logging.Options{
		Level:         current.Logging.Level,
		FileFormat:    current.Logging.FileFormat,
		RotateMaxMB:   current.Logging.RotateMaxMB,
		RotateBackups: current.Logging.RotateBackups,
	})
	if err != nil {
		return fmt.Errorf("构建日志组件失败: %w", err)
	}
	if logCloser != nil {
		defer logCloser.Close()
	}
	logger.Info().Str("version", version).Msg("启动 sing-box 控制面板")
	application, err := app.New(cfg, stop, logger, version, restarter, restoreTrigger)
	if err != nil {
		return fmt.Errorf("创建应用失败: %w", err)
	}
	applicationClosed := false
	defer func() {
		if !applicationClosed {
			_ = application.Close()
		}
	}()

	logger.Info().Msg("数据库迁移完成")
	logger.Info().Str("addr", current.Server.Listen).Str("config", config.NormalizeConfigPath(configPath)).Msg("管理服务已开始监听")
	server := &http.Server{
		Addr:              current.Server.Listen,
		Handler:           application.Router,
		ReadHeaderTimeout: current.Server.ReadHeaderTimeout,
		ReadTimeout:       current.Server.ReadTimeout,
		IdleTimeout:       current.Server.IdleTimeout,
		MaxHeaderBytes:    current.Server.MaxHeaderBytes,
	}
	servers := []managedServer{{name: "management", server: server, serve: server.ListenAndServe}}
	// 面板自带 TLS 直听（server.tls_listen）：证书由面板 ACME 自动签发
	// 续期并热载（经 application.Ops.PanelTLS() 的回调提供）。超时同
	// management——不设 WriteTimeout，SSE 依赖这一点。
	if current.Server.TLSListen != "" {
		tlsConfig := application.Ops.PanelTLS()
		tlsListener, err := tls.Listen("tcp", current.Server.TLSListen, tlsConfig)
		if err != nil {
			return fmt.Errorf("监听面板 TLS 端口 %s 失败: %w", current.Server.TLSListen, err)
		}
		tlsServer := &http.Server{
			Handler:           application.Router,
			ReadHeaderTimeout: current.Server.ReadHeaderTimeout,
			ReadTimeout:       current.Server.ReadTimeout,
			IdleTimeout:       current.Server.IdleTimeout,
			MaxHeaderBytes:    current.Server.MaxHeaderBytes,
		}
		servers = append(servers, managedServer{name: "management-tls", server: tlsServer, serve: func() error { return tlsServer.Serve(tlsListener) }})
		logger.Info().Str("addr", current.Server.TLSListen).Msg("面板 TLS 直听已开始")
	}
	// 80→443 跳转（server.redirect_listen）：纯 301，方便浏览器直达；
	// ACME 走 DNS-01 不依赖此端口。
	if current.Server.RedirectListen != "" {
		redirectTarget := "https://" + current.Deployment.PanelDomain
		redirectServer := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, redirectTarget+r.RequestURI, http.StatusMovedPermanently)
			}),
			ReadHeaderTimeout: current.Server.ReadHeaderTimeout,
			ReadTimeout:       10 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		servers = append(servers, managedServer{name: "http-redirect", server: redirectServer, serve: redirectServer.ListenAndServe})
	}

	results := make(chan serverResult, len(servers))
	for _, item := range servers {
		go func(server managedServer) {
			results <- serverResult{name: server.name, err: server.serve()}
		}(item)
	}

	select {
	case <-signalContext.Done():
		logger.Info().Msg("开始优雅停机")
		// Close SSE streams and background loops first: Shutdown waits for
		// active connections, and request contexts are not cancelled by it,
		// so an open /events stream would block the drain indefinitely.
		application.BeginShutdown()
		shutdownServers(logger, servers)
		for range servers {
			result := <-results
			if result.err != nil && !errors.Is(result.err, http.ErrServerClosed) {
				logger.Warn().Str("server", result.name).Err(result.err).Msg("优雅停机时服务返回错误")
			}
		}
		if err := application.Close(); err != nil {
			logger.Error().Err(err).Msg("优雅停机时关闭应用失败")
		}
		applicationClosed = true
		// 备份恢复的换文件动作只能在数据库 Close 之后执行（WAL 模式下
		// 进程持有库文件句柄）。动作内部失败会自行回滚旧库文件；此时放弃
		// exec、以错误退出，交由 systemd on-failure 拉起旧库，面板照旧可用。
		if action := restartAction.Load(); action != nil {
			restartAction.Store(nil)
			if err := (*action)(); err != nil {
				return fmt.Errorf("恢复数据库替换库文件失败（已回滚）: %w", err)
			}
		}
		if restartRequested.Load() {
			return restartProcess(logger)
		}
		logger.Info().Msg("优雅停机完成")
		return nil
	case result := <-results:
		shutdownServers(logger, servers)
		if result.err == nil || errors.Is(result.err, http.ErrServerClosed) {
			return fmt.Errorf("%s 服务意外停止", result.name)
		}
		return fmt.Errorf("%s 服务启动失败: %w", result.name, result.err)
	}
}

// restartProcess 以替换后的二进制重生面板：此时优雅停机已完成（监听、
// 内核、数据库均已关闭），exec 原地复用进程身份，无需 init/systemd 拉起。
// exec 仅在失败时返回；此时保持 .bak 手动回滚语义，进程以错误退出。
func restartProcess(logger *logging.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位面板二进制失败: %w", err)
	}
	logger.Info().Str("path", exe).Msg("优雅停机完成，正在以新版本重启面板")
	return syscall.Exec(exe, os.Args, os.Environ())
}

func shutdownServers(logger *logging.Logger, servers []managedServer) {
	// No artificial deadline: Shutdown returns as soon as the last in-flight
	// request finishes. Stalled requests are already cut off by the server's
	// own ReadHeaderTimeout/ReadTimeout, so the wait is bounded by the very
	// timeouts the operator configured — not by a magic constant here.
	type shutdownResult struct {
		server managedServer
		err    error
	}
	results := make(chan shutdownResult, len(servers))
	for _, item := range servers {
		go func(server managedServer) {
			results <- shutdownResult{server: server, err: server.server.Shutdown(context.Background())}
		}(item)
	}
	for range servers {
		result := <-results
		if result.err == nil || errors.Is(result.err, http.ErrServerClosed) {
			continue
		}
		logger.Warn().Str("server", result.server.name).Err(result.err).Msg("优雅停机失败，强制关闭")
		if err := result.server.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Str("server", result.server.name).Err(err).Msg("强制关闭失败")
		}
	}
}
