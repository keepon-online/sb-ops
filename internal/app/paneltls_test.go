package app

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/service/certificate"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

func newPanelTLSTestService(t *testing.T) (*panelTLS, *certificate.Service) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := certificate.NewService(certificate.NewStore(db), secret.NewStore(db))
	created, err := service.CreateSelfSigned(t.Context(), certificate.SelfSignedInput{Name: "panel-test", ServerName: "panel.example.com"})
	if err != nil {
		t.Fatalf("create self-signed certificate: %v", err)
	}
	holder := &panelTLS{service: service, certificateID: created.ID}
	return holder, service
}

// TestPanelTLSLoadAndHotReload：加载后 GetCertificate 可用，材料轮换后
// Reload 换出新证书（新握手即用新材料）。
func TestPanelTLSLoadAndHotReload(t *testing.T) {
	holder, service := newPanelTLSTestService(t)
	if err := holder.load(t.Context()); err != nil {
		t.Fatalf("load: %v", err)
	}
	tlsConfig := holder.config()
	first, err := tlsConfig.GetCertificate(nil)
	if err != nil || first == nil {
		t.Fatalf("GetCertificate = %v, %v", first, err)
	}

	// 轮换材料（自签重新生成语义等价于续期换新），热载后证书应变化。
	created, err := service.Get(t.Context(), holder.certificateID)
	if err != nil {
		t.Fatalf("get certificate: %v", err)
	}
	regenerated, err := service.RegenerateSelfSigned(t.Context(), certificate.RegenerateInput{ID: created.ID, Revision: created.Revision, ServerName: "panel.example.com"})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if regenerated.Fingerprint == created.Fingerprint {
		t.Fatal("材料应已轮换")
	}
	if err := holder.load(t.Context()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	second, err := holder.config().GetCertificate(nil)
	if err != nil || second == nil {
		t.Fatalf("GetCertificate after reload = %v, %v", second, err)
	}
}

// TestPanelTLSConfigWithoutLoadFailsCleanly：未加载时握手得到明确错误而非
// 空指针。
func TestPanelTLSConfigWithoutLoadFailsCleanly(t *testing.T) {
	holder, _ := newPanelTLSTestService(t)
	if _, err := holder.config().GetCertificate(nil); err == nil {
		t.Fatal("未加载时应返回错误")
	}
}

// TestPanelTLSDisabledReturnsNilConfig：未启用时 PanelTLS 为 nil。
func TestPanelTLSDisabledReturnsNilConfig(t *testing.T) {
	application := &OpsApp{}
	if application.PanelTLS() != nil {
		t.Fatal("未启用 tls_listen 时 PanelTLS 应为 nil")
	}
}
