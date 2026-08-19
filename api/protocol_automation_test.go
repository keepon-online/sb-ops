package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

func newProtocolAutomationRouter(t *testing.T) (*sql.DB, *gin.Engine) {
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
	identityStore := identity.NewStore(db)
	identityService := identity.NewService(identityStore, time.Hour)
	if _, err := identityService.EnsureBootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	secrets := secret.NewStore(db)
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{
		Identity: identityService, Store: identityStore,
		Protocols:    serviceprotocol.NewService(serviceprotocol.NewStore(db, secrets)),
		Certificates: servicecertificate.NewService(servicecertificate.NewStore(db), secrets),
	})
	return db, router
}

func TestProtocolAutomationEndpoints(t *testing.T) {
	db, router := newProtocolAutomationRouter(t)
	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)
	changed := performJSON(t, router, http.MethodPost, "/api/v1/auth/change-password", `{"currentPassword":"admin123","newPassword":"Changed123!"}`, session.Token)
	if changed.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", changed.Code, changed.Body.String())
	}
	login = performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"Changed123!"}`, "")
	decodeResponse(t, login, &session)

	protocols := serviceprotocol.NewService(serviceprotocol.NewStore(db, secret.NewStore(db)))
	if _, err := protocols.Create(t.Context(), serviceprotocol.WriteInput{
		Name: "TCP 443", Type: serviceprotocol.TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 443,
		Settings: map[string]any{"method": "2022-blake3-aes-128-gcm"},
	}); err != nil {
		t.Fatalf("create protocol: %v", err)
	}

	suggestion := performJSON(t, router, http.MethodPost, "/api/v1/protocols/suggest-port", `{"type":"vless"}`, session.Token)
	if suggestion.Code != http.StatusOK || !bytes.Contains(suggestion.Body.Bytes(), []byte(`"listenPort":8443`)) {
		t.Fatalf("suggestion status=%d body=%s", suggestion.Code, suggestion.Body.String())
	}
	certificate := performJSON(t, router, http.MethodPost, "/api/v1/certificates/self-signed", `{"name":"generated","serverName":"panel.example.com"}`, session.Token)
	if certificate.Code != http.StatusCreated || bytes.Contains(certificate.Body.Bytes(), []byte("privateKey")) || bytes.Contains(certificate.Body.Bytes(), []byte("certificatePem")) {
		t.Fatalf("certificate status=%d body=%s", certificate.Code, certificate.Body.String())
	}
	var created servicecertificate.Certificate
	decodeResponse(t, certificate, &created)
	if created.ID == "" {
		t.Fatalf("certificate response=%+v", created)
	}
	if _, err := servicecertificate.NewService(servicecertificate.NewStore(db), secret.NewStore(db)).Material(t.Context(), created.ID); err != nil {
		t.Fatalf("stored certificate material: %v", err)
	}
	regenerated := performJSON(t, router, http.MethodPost, "/api/v1/certificates/"+created.ID+"/regenerate", `{"revision":1,"serverName":"198.51.100.10"}`, session.Token)
	if regenerated.Code != http.StatusOK || bytes.Contains(regenerated.Body.Bytes(), []byte("privateKey")) {
		t.Fatalf("regenerate status=%d body=%s", regenerated.Code, regenerated.Body.String())
	}
	var updated servicecertificate.Certificate
	decodeResponse(t, regenerated, &updated)
	if updated.ID != created.ID || updated.Revision != created.Revision+1 {
		t.Fatalf("regenerated certificate=%+v", updated)
	}
}
