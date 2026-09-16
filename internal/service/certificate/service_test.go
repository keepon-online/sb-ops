package certificate

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"path/filepath"
	"strings"
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

// createTestACMECertificate 经 selfSignedMaterial 产出合法材料建一张 ACME
// 来源证书（材料本身无所谓，签发链在测试里被覆写）。
func createTestACMECertificate(t *testing.T, service *Service, name string) Certificate {
	t.Helper()
	certificatePEM, privateKeyPEM, err := service.selfSignedMaterial("panel.example.com")
	if err != nil {
		t.Fatalf("generate material: %v", err)
	}
	created, err := service.Create(t.Context(), WriteInput{
		Name: name, Source: SourceACME, Domain: "panel.example.com",
		CertificatePEM: certificatePEM, PrivateKeyPEM: privateKeyPEM,
	})
	if err != nil {
		t.Fatalf("create ACME certificate: %v", err)
	}
	return created
}

// TestRenewACMEUpdatesCertificateInPlace 是续期路径的回归锚：续期必须对原行
// 原地 Update（保 ID、保 certificate_id 引用），走「新建同名行」会撞 name
// 唯一约束必然失败（曾经的 H1）。
func TestRenewACMEUpdatesCertificateInPlace(t *testing.T) {
	service := newTestCertificateService(t)
	created := createTestACMECertificate(t, service, "panel-acme")

	renewedPEM, renewedKeyPEM, err := service.selfSignedMaterial("panel.example.com")
	if err != nil {
		t.Fatalf("generate renewed material: %v", err)
	}
	service.issue = func(ctx context.Context, input AcmeInput, options AcmeOptions) (WriteInput, error) {
		if input.Name != "panel-acme" || input.Domain != "panel.example.com" {
			t.Errorf("issue input = %+v, want stored name/domain", input)
		}
		return WriteInput{Name: input.Name, Source: SourceACME, Domain: input.Domain, CertificatePEM: renewedPEM, PrivateKeyPEM: renewedKeyPEM}, nil
	}

	renewed, err := service.RenewACME(t.Context(), created.ID, AcmeOptions{Email: "admin@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("renew ACME certificate: %v", err)
	}
	if renewed.ID != created.ID {
		t.Fatalf("renewal must keep row ID: %s != %s", renewed.ID, created.ID)
	}
	if renewed.Revision != created.Revision+1 {
		t.Fatalf("revision = %d, want %d", renewed.Revision, created.Revision+1)
	}
	if renewed.Fingerprint == created.Fingerprint {
		t.Fatal("renewal must rotate material")
	}
	if renewed.Domain != "panel.example.com" || renewed.Source != SourceACME {
		t.Fatalf("renewed certificate = %+v", renewed)
	}
}

// TestRenewDueACMEContinuesPastSingleFailure：一张证书续期失败不得阻断其余
// 证书的续期窗口（曾经的 H1 连带问题——首个失败即整体中断）。
func TestRenewDueACMEContinuesPastSingleFailure(t *testing.T) {
	service := newTestCertificateService(t)
	service.now = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
	stuck := createTestACMECertificate(t, service, "stuck")
	healthy := createTestACMECertificate(t, service, "healthy")

	// 推进时钟让两张证书都进入 30 天续期窗口。
	service.now = func() time.Time { return time.Date(2027, 9, 1, 0, 0, 0, 0, time.UTC) }
	renewedPEM, renewedKeyPEM, err := service.selfSignedMaterial("panel.example.com")
	if err != nil {
		t.Fatalf("generate renewed material: %v", err)
	}
	service.issue = func(ctx context.Context, input AcmeInput, options AcmeOptions) (WriteInput, error) {
		if input.Name == "stuck" {
			return WriteInput{}, errors.New("DNS-01 挑战超时")
		}
		return WriteInput{Name: input.Name, Source: SourceACME, Domain: input.Domain, CertificatePEM: renewedPEM, PrivateKeyPEM: renewedKeyPEM}, nil
	}

	renewed, err := service.RenewDueACME(t.Context(), AcmeOptions{Email: "admin@example.com", APIToken: "token"}, 30*24*time.Hour)
	if renewed != 1 {
		t.Fatalf("renewed = %d, want 1", renewed)
	}
	if err == nil || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("renewal error = %v, want failure mentioning stuck certificate", err)
	}
	after, err := service.Get(t.Context(), healthy.ID)
	if err != nil {
		t.Fatalf("read healthy certificate: %v", err)
	}
	if after.Fingerprint == healthy.Fingerprint {
		t.Fatal("healthy certificate was not renewed")
	}
	untouched, err := service.Get(t.Context(), stuck.ID)
	if err != nil {
		t.Fatalf("read stuck certificate: %v", err)
	}
	if untouched.Fingerprint != stuck.Fingerprint {
		t.Fatal("failed certificate must keep its old material")
	}
}

// TestUpdatePreservesDomainWhenOmitted：API 更新请求不携带 domain 字段，
// Update 须继承既有值——落空会让 ACME 证书永久退出自动续期（L1）。
func TestUpdatePreservesDomainWhenOmitted(t *testing.T) {
	service := newTestCertificateService(t)
	created := createTestACMECertificate(t, service, "panel-acme")

	replacementPEM, replacementKey, err := service.selfSignedMaterial("panel.example.com")
	if err != nil {
		t.Fatalf("generate replacement material: %v", err)
	}
	updated, err := service.Update(t.Context(), WriteInput{
		ID: created.ID, Revision: created.Revision, Name: created.Name,
		CertificatePEM: replacementPEM, PrivateKeyPEM: replacementKey,
	})
	if err != nil {
		t.Fatalf("update certificate: %v", err)
	}
	if updated.Domain != "panel.example.com" {
		t.Fatalf("domain = %q, want inherited panel.example.com", updated.Domain)
	}
	if updated.Source != SourceACME {
		t.Fatalf("source = %q, want inherited acme", updated.Source)
	}
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
