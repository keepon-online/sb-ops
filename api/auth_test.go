package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
)

func TestSetupCreatesFirstAdminOnlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	store := identity.NewStore(db)
	service := identity.NewService(store, time.Hour)
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: service, Store: store})

	status := performJSON(t, router, http.MethodGet, "/api/v1/setup/status", "", "")
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"initialized":false`)) {
		t.Fatalf("initial status=%d body=%s", status.Code, status.Body.String())
	}
	created := performJSON(t, router, http.MethodPost, "/api/v1/setup/admin", `{"username":"owner","password":"OwnerPass123"}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create status=%d body=%s", created.Code, created.Body.String())
	}
	duplicate := performJSON(t, router, http.MethodPost, "/api/v1/setup/admin", `{"username":"second","password":"SecondPass123"}`, "")
	assertErrorCode(t, duplicate, http.StatusConflict, httpx.CodeAlreadyInitialized)

	status = performJSON(t, router, http.MethodGet, "/api/v1/setup/status", "", "")
	if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte(`"initialized":true`)) {
		t.Fatalf("final status=%d body=%s", status.Code, status.Body.String())
	}
	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"owner","password":"OwnerPass123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("setup login status=%d body=%s", login.Code, login.Body.String())
	}
}

func TestChangePasswordRevokesOldSession(t *testing.T) {
	router, _, _ := newIdentityRouter(t)

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var loginBody loginResponse
	decodeResponse(t, login, &loginBody)
	if loginBody.Token == "" {
		t.Fatalf("login response=%+v", loginBody)
	}

	changed := performJSON(t, router, http.MethodPost, "/api/v1/auth/change-password", `{"currentPassword":"admin123","newPassword":"Changed123!"}`, loginBody.Token)
	if changed.Code != http.StatusOK {
		t.Fatalf("change status=%d body=%s", changed.Code, changed.Body.String())
	}

	oldSession := performJSON(t, router, http.MethodGet, "/api/v1/auth/me", "", loginBody.Token)
	assertErrorCode(t, oldSession, http.StatusUnauthorized, httpx.CodeUnauthorized)

	newLogin := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"Changed123!"}`, "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new login status=%d body=%s", newLogin.Code, newLogin.Body.String())
	}
	decodeResponse(t, newLogin, &loginBody)
	me := performJSON(t, router, http.MethodGet, "/api/v1/auth/me", "", loginBody.Token)
	if me.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}

	setup := performJSON(t, router, http.MethodGet, "/api/setup/status", "", "")
	if setup.Code != http.StatusNotFound {
		t.Fatalf("setup status=%d body=%s", setup.Code, setup.Body.String())
	}
}

func TestClientAddressTrustsForwardedHeadersOnlyFromTrustedProxy(t *testing.T) {
	// 与生产 RegisterRoutes 相同的可信代理配置：只有回环反代注入的
	// X-Forwarded-For 才被采信（设备指纹与登录限速都依赖来源地址）。
	newContext := func(remoteAddr string) *gin.Context {
		c, engine := gin.CreateTestContext(httptest.NewRecorder())
		if err := engine.SetTrustedProxies([]string{"127.0.0.1", "::1"}); err != nil {
			t.Fatalf("set trusted proxies: %v", err)
		}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		c.Request.RemoteAddr = remoteAddr
		c.Request.Header.Set("X-Forwarded-For", "203.0.113.99")
		return c
	}

	// 直连（对端不是可信代理）：XFF 是客户端自带的可伪造头，必须忽略。
	direct := newContext("192.0.2.10:43210")
	if got := clientAddress(direct); got != "192.0.2.10" {
		t.Fatalf("direct clientAddress=%q, want socket peer", got)
	}
	// 经同机反代（对端回环）：XFF 由 Nginx 追加真实来源，应采信——否则
	// 固定反代部署下所有请求的来源都塌缩成 127.0.0.1，登录限速退化为
	// 全局桶、设备指纹的 IP 维度失效。
	proxied := newContext("127.0.0.1:51234")
	if got := clientAddress(proxied); got != "203.0.113.99" {
		t.Fatalf("proxied clientAddress=%q, want forwarded peer", got)
	}
}

func newIdentityRouter(t *testing.T) (*gin.Engine, *identity.Service, *identity.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	store := identity.NewStore(db)
	service := identity.NewService(store, time.Hour)
	if _, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: service, Store: store})
	return router, service, store
}

func performJSON(t *testing.T, router http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Request-ID", "request-12345678")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// decodeResponse 解开统一响应信封，把 data 反序列化进 target；断言错误码时
// 用 decodeEnvelope 直接读信封层。
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	envelope := decodeEnvelope(t, rec)
	raw, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("re-encode data: %v", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode data: %v body=%s", err, rec.Body.String())
	}
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) httpx.Response {
	t.Helper()
	var envelope httpx.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, rec.Body.String())
	}
	return envelope
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code httpx.Code) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, status, rec.Body.String())
	}
	response := decodeEnvelope(t, rec)
	if response.Code != code || response.RequestID != "request-12345678" {
		t.Fatalf("error=%+v", response)
	}
}
