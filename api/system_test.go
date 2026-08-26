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
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
)

func TestSystemUpgradeEndpointsRequireAuthAndMapErrors(t *testing.T) {
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
	upgrade := controlupgrade.NewService(func() (string, string) { return "", "" }, "v0.2.0", nil, nil)
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: identityService, Store: identityStore, Upgrade: upgrade})

	unauthenticated := performJSON(t, router, http.MethodGet, "/api/v1/system/upgrade/status", "", "")
	assertErrorCode(t, unauthenticated, http.StatusUnauthorized, httpx.CodeUnauthorized)

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)

	response := performJSON(t, router, http.MethodGet, "/api/v1/system/upgrade/status", "", session.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot controlupgrade.Status
	decodeResponse(t, response, &snapshot)
	if snapshot.Configured || snapshot.CurrentVersion != "v0.2.0" || snapshot.Phase != controlupgrade.PhaseIdle {
		t.Fatalf("未配置升级源时应返回 idle 快照：%+v", snapshot)
	}

	check := performJSON(t, router, http.MethodPost, "/api/v1/system/upgrade/check", "", session.Token)
	assertErrorCode(t, check, http.StatusBadRequest, httpx.CodeUpgradeNotConfigured)
	start := performJSON(t, router, http.MethodPost, "/api/v1/system/upgrade/start", "", session.Token)
	assertErrorCode(t, start, http.StatusBadRequest, httpx.CodeUpgradeNotConfigured)
}
