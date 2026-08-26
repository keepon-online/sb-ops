package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/database"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	"github.com/keepon-online/sb-ops/internal/logging"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
	"github.com/keepon-online/sb-ops/internal/web"
	_ "github.com/mattn/go-sqlite3"

	"github.com/gin-gonic/gin"
)

type App struct {
	Config *config.RuntimeConfigHolder
	Ops    *OpsApp
	Router *gin.Engine
	stop   func()
}

// New 装配控制面板。version 是构建期注入的面板版本（main.version），
// restart 是自升级完成后触发进程重生的钩子（置重启标志并向自身发
// SIGTERM，由 main 的优雅停机路径收尾后 exec 新二进制）。
func New(cfg *config.RuntimeConfigHolder, stop func(), logger *logging.Logger, version string, restart func()) (*App, error) {
	if logger == nil {
		logger = logging.DefaultLogger()
	}
	current := cfg.Current()
	db, err := openOpsDB(current.Server.DBPath, current.Server.DBMMapSize, current.Server.DBSynchronous)
	if err != nil {
		return nil, err
	}
	cacheFilePath := filepath.Join(filepath.Dir(current.SingBox.CertificateDir), "singbox-cache.db")
	// sing-box opens the cache file directly and does not create its parent
	// directory, so ensure it exists before the embedded core first applies.
	if err := os.MkdirAll(filepath.Dir(cacheFilePath), 0o750); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("创建 sing-box 缓存目录: %w", err)
	}
	apply := deployment.ApplyConfig{
		CertificateDir:      current.SingBox.CertificateDir,
		PublicAddress:       current.Deployment.PublicAddress,
		TLSCertPath:         current.Deployment.TLSCertPath,
		TLSKeyPath:          current.Deployment.TLSKeyPath,
		CacheFilePath:       cacheFilePath,
		AllowPrivateAddress: current.Development.AllowPrivateAddress,
	}
	collector := controltraffic.CollectorConfig{
		Interval: current.Traffic.Interval,
	}
	opsApp, err := newOps(db, current.Panel.SessionTTL, apply, collector, cfg.Current, version, restart, logger)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	web.RegisterFrontendRoutes(opsApp.Router, cfg)
	logger.Info().Msg("控制面板应用初始化完成")
	return &App{Config: cfg, Ops: opsApp, Router: opsApp.Router, stop: stop}, nil
}

// BeginShutdown stops SSE streams and background loops ahead of the HTTP
// drain; see OpsApp.BeginShutdown for why the order matters.
func (a *App) BeginShutdown() {
	if a.Ops != nil {
		a.Ops.BeginShutdown()
	}
}

func (a *App) Close() error {
	if a.stop != nil {
		a.stop()
	}
	var closeErr error
	if a.Ops != nil {
		if err := a.Ops.Close(); closeErr == nil {
			closeErr = err
		}
		a.Ops = nil
	}
	return closeErr
}

func openOpsDB(path string, mmapSize int64, synchronous string) (*sql.DB, error) {
	return database.Open(database.Options{Path: path, MMapSize: mmapSize, Synchronous: synchronous})
}
