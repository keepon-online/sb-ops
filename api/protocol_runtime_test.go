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
	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

func newProtocolRuntimeRouter(t *testing.T, withSync bool) (*gin.Engine, string) {
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
	router, token := newProtocolRuntimeRouter(t, false)
	response := performJSON(t, router, http.MethodGet, "/api/v1/protocols/runtime-status", "", token)
	assertErrorCode(t, response, http.StatusServiceUnavailable, httpx.CodeRuntimeUnavailable)
}

func TestProtocolRuntimeStatusReturnsItems(t *testing.T) {
	router, token := newProtocolRuntimeRouter(t, true)
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
