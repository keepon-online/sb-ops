package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	controlmonitor "github.com/keepon-online/sb-ops/internal/monitor"
)

func TestMonitorStatusRequiresAuthAndReturnsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	identityStore := identity.NewStore(db)
	identityService := identity.NewService(identityStore, time.Hour)
	if _, err := identityService.EnsureBootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	monitor := controlmonitor.NewService()
	// 直接完成一次采样（真实 gopsutil 数据源），断言接口输出完整快照。
	if err := monitor.Sample(t.Context()); err != nil {
		t.Fatalf("sample monitor: %v", err)
	}
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: identityService, Store: identityStore, Monitor: monitor})

	unauthenticated := performJSON(t, router, http.MethodGet, "/api/v1/monitor", "", "")
	assertErrorCode(t, unauthenticated, http.StatusUnauthorized, httpx.CodeUnauthorized)

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)

	response := performJSON(t, router, http.MethodGet, "/api/v1/monitor", "", session.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("monitor status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot controlmonitor.Snapshot
	decodeResponse(t, response, &snapshot)
	if !snapshot.Ready || snapshot.SampledAt.IsZero() {
		t.Fatalf("快照应已就绪：%+v", snapshot)
	}
	if snapshot.Host.OS == "" || snapshot.CPU.LogicalCores <= 0 {
		t.Fatalf("真实数据源应填充主机与 CPU 信息：%+v %+v", snapshot.Host, snapshot.CPU)
	}
	if len(snapshot.History) != 1 {
		t.Fatalf("单次采样后历史应有 1 个点，got %d", len(snapshot.History))
	}
}
