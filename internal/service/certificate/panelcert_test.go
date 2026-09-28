package certificate

import (
	"context"
	"testing"
)

// TestEnsurePanelCertificateIssuesOnceThenReuses：首次调用经签发链建
// panel-tls 证书，之后调用必须复用既有行（不再走签发）。
func TestEnsurePanelCertificateIssuesOnceThenReuses(t *testing.T) {
	service := newTestCertificateService(t)

	issues := 0
	service.issue = func(ctx context.Context, input AcmeInput, options AcmeOptions) (WriteInput, error) {
		issues++
		certificatePEM, privateKeyPEM, err := service.selfSignedMaterial("panel.example.com")
		if err != nil {
			t.Fatalf("generate material: %v", err)
		}
		return WriteInput{Name: input.Name, Source: SourceACME, Domain: input.Domain, CertificatePEM: certificatePEM, PrivateKeyPEM: privateKeyPEM}, nil
	}

	first, err := service.EnsurePanelCertificate(t.Context(), "panel.example.com", AcmeOptions{Email: "admin@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("首次确保面板证书: %v", err)
	}
	if first.Name != PanelCertificateName || first.Source != SourceACME {
		t.Fatalf("first = %+v", first)
	}

	second, err := service.EnsurePanelCertificate(t.Context(), "panel.example.com", AcmeOptions{Email: "admin@example.com", APIToken: "token"})
	if err != nil {
		t.Fatalf("二次确保面板证书: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("应复用既有行: %s != %s", second.ID, first.ID)
	}
	if issues != 1 {
		t.Fatalf("签发应只发生一次，实际 %d 次", issues)
	}
}
