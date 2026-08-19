package compiler

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/keepon-online/sb-ops/internal/database"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

func TestLoadInputRequiresPublicAddress(t *testing.T) {
	db := newLoaderDatabase(t)
	_, err := LoadInput(t.Context(), db, Deployment{})
	if err == nil || err.Error() != "公网地址未配置" {
		t.Fatalf("LoadInput() error = %v, want public address configuration error", err)
	}
}

func TestLoadInputMaterializesReferencedCertificate(t *testing.T) {
	db := newLoaderDatabase(t)
	certificates := servicecertificate.NewService(servicecertificate.NewStore(db), secret.NewStore(db))
	certificate, err := certificates.CreateSelfSigned(t.Context(), servicecertificate.SelfSignedInput{Name: "generated", ServerName: "panel.example.com"})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	protocols := serviceprotocol.NewService(serviceprotocol.NewStore(db, secret.NewStore(db)))
	if _, err := protocols.Create(t.Context(), serviceprotocol.WriteInput{
		Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, Listen: "::", ListenPort: 2443,
		CertificateID: &certificate.ID, Settings: map[string]any{"serverName": "panel.example.com", "upMbps": 100, "downMbps": 100},
	}); err != nil {
		t.Fatalf("create protocol: %v", err)
	}

	certificateDir := filepath.Join(t.TempDir(), "certificates")
	input, err := LoadInput(t.Context(), db, Deployment{PublicAddress: "panel.example.com"}, certificateDir)
	if err != nil {
		t.Fatalf("LoadInput: %v", err)
	}
	paths, ok := input.CertificatePaths[certificate.ID]
	if !ok {
		t.Fatalf("certificate paths = %#v", input.CertificatePaths)
	}
	if want := filepath.Join(certificateDir, certificate.ID, "1", "cert.pem"); paths.CertificatePath != want {
		t.Fatalf("certificate path=%q want=%q", paths.CertificatePath, want)
	}
	if _, err := os.Stat(paths.CertificatePath); err != nil {
		t.Fatalf("certificate material missing: %v", err)
	}
	if _, err := os.Stat(paths.KeyPath); err != nil {
		t.Fatalf("private key material missing: %v", err)
	}
}

func newLoaderDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return db
}
