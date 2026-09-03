package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	controlprobe "github.com/keepon-online/sb-ops/internal/probe"
	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

// newProtocolRuntimeRouter 装配带协议运行时端点的测试路由。ready 非空时
// 一并装配拨测 Prober（共享同一数据库，读到已种子的 proto-a 协议）。
func newProtocolRuntimeRouter(t *testing.T, withSync bool, ready func(context.Context) (bool, string)) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Options{Path: t.TempDir() + "/control.db"})
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
	secrets := secret.NewStore(db)
	protocols := serviceprotocol.NewService(serviceprotocol.NewStore(db, secrets))
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if err := serviceprotocol.NewStore(db, secrets).Create(t.Context(), serviceprotocol.Protocol{
		ID: "proto-a", Name: "alpha", Type: serviceprotocol.TypeVLESS, Enabled: true,
		Listen: "::", ListenPort: 8443, Settings: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed protocol: %v", err)
	}

	dependencies := Dependencies{
		Identity: identityService, Store: identityStore,
		Protocols:    protocols,
		Certificates: servicecertificate.NewService(servicecertificate.NewStore(db), secrets),
	}
	if ready != nil {
		dependencies.Prober = controlprobe.NewProber(db, ready)
	}
	if withSync {
		syncer := deployment.NewSyncService(db, deployment.ApplyConfig{})
		syncer.KernelState = func() runtimebox.Status { return runtimebox.Status{State: "running"} }
		syncer.ListenSnapshot = func(context.Context) (deployment.ListenSnapshot, error) {
			return deployment.ListenSnapshot{TCP: map[int]struct{}{8443: {}}}, nil
		}
		dependencies.Sync = syncer
	}
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, dependencies)

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)
	return router, session.Token
}

func TestProtocolRuntimeStatusWithoutSyncService(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, false, nil)
	response := performJSON(t, router, http.MethodGet, "/api/v1/protocols/runtime-status", "", token)
	assertErrorCode(t, response, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable)
}

func TestProtocolRuntimeStatusReturnsItems(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, true, nil)
	response := performJSON(t, router, http.MethodGet, "/api/v1/protocols/runtime-status", "", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result deployment.ProtocolRuntimeStatusResult
	decodeResponse(t, response, &result)
	if result.KernelStatus != "running" || !result.ListeningAvailable || result.ConnectionsAvailable {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %#v", result.Items)
	}
	item := result.Items[0]
	if item.ID != "proto-a" || item.Status != "listening" || item.ActiveConnections != nil {
		t.Fatalf("item = %#v", item)
	}
}

func TestProbeEndpointsWithoutProber(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, false, nil)
	response := performJSON(t, router, http.MethodGet, "/api/v1/protocols/probe-results", "", token)
	assertErrorCode(t, response, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable)
	trigger := performJSON(t, router, http.MethodPost, "/api/v1/protocols/probe", "", token)
	assertErrorCode(t, trigger, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable)
}

// TestManualProbeRunsAndPersists 手动拨测（不起内核）：协议没有分配设备时
// 返回 skipped 并落库，GET 读取面回读同一行；单协议过滤只返回该协议。
func TestManualProbeRunsAndPersists(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, true, func(context.Context) (bool, string) { return true, "" })

	response := performJSON(t, router, http.MethodPost, "/api/v1/protocols/probe?protocolId=proto-a", "", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result controlprobe.Result
	decodeResponse(t, response, &result)
	if len(result.Items) != 1 || result.Items[0].ProtocolID != "proto-a" || result.Items[0].Status != "skipped" {
		t.Fatalf("items = %#v", result.Items)
	}

	stored := performJSON(t, router, http.MethodGet, "/api/v1/protocols/probe-results", "", token)
	if stored.Code != http.StatusOK {
		t.Fatalf("stored status=%d body=%s", stored.Code, stored.Body.String())
	}
	var storedResult controlprobe.StoredResult
	decodeResponse(t, stored, &storedResult)
	if len(storedResult.Items) != 1 || storedResult.Items[0].Status != "skipped" {
		t.Fatalf("stored items = %#v", storedResult.Items)
	}
}

// TestManualProbeGatedOnKernelReady 内核未就绪时手动拨测返回 503 而非失败行。
func TestManualProbeGatedOnKernelReady(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, true, func(context.Context) (bool, string) { return false, "内核未运行" })
	response := performJSON(t, router, http.MethodPost, "/api/v1/protocols/probe", "", token)
	assertErrorCode(t, response, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable)
}
