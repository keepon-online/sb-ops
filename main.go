package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

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
	application, err := app.New(cfg, stop, logger)
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
