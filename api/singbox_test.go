package api

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

func TestSingBoxRoutesRequireAuthenticationAndMaskOutboundPasswords(t *testing.T) {
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
	account, err := identityService.EnsureBootstrapAdmin(t.Context(), "admin", "admin123")
	if err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	if _, err := identityService.ChangePassword(t.Context(), account.ID, "admin123", "Changed123!"); err != nil {
		t.Fatalf("change bootstrap password: %v", err)
	}
	singBox := servicesingbox.NewService(servicesingbox.NewStore(db, secret.NewStore(db)))
	password := "upstream-secret"
	outbound, err := singBox.CreateOutbound(t.Context(), servicesingbox.OutboundWriteInput{Tag: "proxy", Server: "127.0.0.1", ServerPort: 1080, Password: &password})
	if err != nil {
		t.Fatalf("create outbound: %v", err)
	}
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: identityService, Store: identityStore, SingBox: singBox})

	unauthenticated := performJSON(t, router, http.MethodGet, "/api/v1/singbox/outbounds", "", "")
	assertErrorCode(t, unauthenticated, http.StatusUnauthorized, httpx.CodeUnauthorized)
	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"Changed123!"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)
	response := performJSON(t, router, http.MethodGet, "/api/v1/singbox/outbounds", "", session.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("list outbounds status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Body.String() == password || !containsOutbound(response.Body.Bytes(), outbound.ID, true) {
		t.Fatalf("outbounds response leaked secret or omitted outbound: %s", response.Body.String())
	}
}

func containsOutbound(body []byte, id string, hasPassword bool) bool {
	return string(body) != "" && bytes.Contains(body, []byte(`"id":"`+id+`"`)) && bytes.Contains(body, []byte(`"hasPassword":`+strconv.FormatBool(hasPassword)))
}
