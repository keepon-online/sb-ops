package certificate

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

func TestCreateSelfSignedCertificateStoresIPAndDNSNames(t *testing.T) {
	service := newTestCertificateService(t)
	service.now = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }

	ipCertificate, err := service.CreateSelfSigned(t.Context(), SelfSignedInput{Name: "IP certificate", ServerName: "198.51.100.10"})
	if err != nil {
		t.Fatalf("create IP certificate: %v", err)
	}
	ipMaterial, err := service.Material(t.Context(), ipCertificate.ID)
	if err != nil {
		t.Fatalf("read IP material: %v", err)
	}
	assertSelfSignedCertificate(t, ipMaterial.CertificatePEM, ipMaterial.PrivateKeyPEM, "", net.ParseIP("198.51.100.10"))
	if !ipCertificate.NotAfter.Equal(time.Date(2027, 7, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("IP certificate expires at %s", ipCertificate.NotAfter)
	}

	dnsCertificate, err := service.CreateSelfSigned(t.Context(), SelfSignedInput{Name: "DNS certificate", ServerName: "panel.example.com"})
	if err != nil {
		t.Fatalf("create DNS certificate: %v", err)
	}
	dnsMaterial, err := service.Material(t.Context(), dnsCertificate.ID)
	if err != nil {
		t.Fatalf("read DNS material: %v", err)
	}
	assertSelfSignedCertificate(t, dnsMaterial.CertificatePEM, dnsMaterial.PrivateKeyPEM, "panel.example.com", nil)

	if _, err := service.CreateSelfSigned(t.Context(), SelfSignedInput{Name: "invalid", ServerName: "https://panel.example.com"}); err == nil {
		t.Fatal("invalid server name accepted")
	}
}

func TestRegenerateSelfSignedPreservesCertificateID(t *testing.T) {
	service := newTestCertificateService(t)
	created, err := service.CreateSelfSigned(t.Context(), SelfSignedInput{Name: "certificate", ServerName: "panel.example.com"})
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	regenerated, err := service.RegenerateSelfSigned(t.Context(), RegenerateInput{ID: created.ID, Revision: created.Revision, ServerName: "198.51.100.10"})
	if err != nil {
		t.Fatalf("regenerate certificate: %v", err)
	}
	if regenerated.ID != created.ID || regenerated.Revision != created.Revision+1 || regenerated.Fingerprint == created.Fingerprint {
		t.Fatalf("regenerated certificate=%+v created=%+v", regenerated, created)
	}
	material, err := service.Material(t.Context(), regenerated.ID)
	if err != nil {
		t.Fatalf("read regenerated material: %v", err)
	}
	assertSelfSignedCertificate(t, material.CertificatePEM, material.PrivateKeyPEM, "", net.ParseIP("198.51.100.10"))
}

func assertSelfSignedCertificate(t *testing.T, certificatePEM, privateKeyPEM, dnsName string, ip net.IP) {
	t.Helper()
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil {
		t.Fatal("certificate PEM missing")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if dnsName != "" && (len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != dnsName) {
		t.Fatalf("DNS names = %#v", certificate.DNSNames)
	}
	if ip != nil && (len(certificate.IPAddresses) != 1 || !certificate.IPAddresses[0].Equal(ip)) {
		t.Fatalf("IP addresses = %#v", certificate.IPAddresses)
	}
	if _, _, fields := parseMaterial(certificatePEM, privateKeyPEM); len(fields) != 0 {
		t.Fatalf("stored certificate material is invalid: %v", fields)
	}
}

func newTestCertificateService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return NewService(NewStore(db), secret.NewStore(db))
}
