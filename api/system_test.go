package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
	controlwarphealth "github.com/keepon-online/sb-ops/internal/warphealth"
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

// TestSystemWarpRestartEndpointAuthAndErrorMapping 覆盖 WARP 服务重启端点的
// 认证、未接线 503 与错误映射（未部署 404 / 执行失败 502）。
func TestSystemWarpRestartEndpointAuthAndErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newRouter := func(t *testing.T, restart func(context.Context) (controlwarphealth.Result, error)) (*gin.Engine, string) {
		t.Helper()
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
		deps := Dependencies{Identity: identityService, Store: identityStore}
		if restart != nil {
			deps.WarpRestartService = restart
		}
		router := gin.New()
		router.Use(httpx.RequestIDMiddleware())
		RegisterRoutes(router, deps)
		login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
		if login.Code != http.StatusOK {
			t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
		}
		var session loginResponse
		decodeResponse(t, login, &session)
		return router, session.Token
	}

	router, _ := newRouter(t, nil)
	unauthenticated := performJSON(t, router, http.MethodPost, "/api/v1/system/warp/restart", "", "")
	assertErrorCode(t, unauthenticated, http.StatusUnauthorized, httpx.CodeUnauthorized)

	router, token := newRouter(t, nil)
	assertErrorCode(t, performJSON(t, router, http.MethodPost, "/api/v1/system/warp/restart", "", token),
		http.StatusServiceUnavailable, httpx.CodeWarpRestartUnavailable)

	router, token = newRouter(t, func(context.Context) (controlwarphealth.Result, error) {
		return controlwarphealth.Result{}, controlwarphealth.ErrWarpNotDeployed
	})
	assertErrorCode(t, performJSON(t, router, http.MethodPost, "/api/v1/system/warp/restart", "", token),
		http.StatusNotFound, httpx.CodeNotFound)

	router, token = newRouter(t, func(context.Context) (controlwarphealth.Result, error) {
		return controlwarphealth.Result{}, errors.New("重启 warp-svc 失败: systemctl 卡死")
	})
	assertErrorCode(t, performJSON(t, router, http.MethodPost, "/api/v1/system/warp/restart", "", token),
		http.StatusBadGateway, httpx.CodeWarpRestartFailed)

	router, token = newRouter(t, func(context.Context) (controlwarphealth.Result, error) {
		return controlwarphealth.Result{Verdict: controlwarphealth.VerdictOK, WarpOn: true, ExitFamily: "ipv6"}, nil
	})
	response := performJSON(t, router, http.MethodPost, "/api/v1/system/warp/restart", "", token)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result controlwarphealth.Result
	decodeResponse(t, response, &result)
	if result.Verdict != controlwarphealth.VerdictOK || result.ExitFamily != "ipv6" {
		t.Fatalf("result=%+v", result)
	}
}
