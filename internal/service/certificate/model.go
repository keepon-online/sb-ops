package certificate

import "time"

// Certificate provenance. Self-signed and manual certificates are created by
// the administrator; acme certificates are issued and renewed via Let's
// Encrypt DNS-01 against a domain.
const (
	SourceSelfSigned = "self_signed"
	SourceManual     = "manual"
	SourceACME       = "acme"
)

type Certificate struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Source      string    `json:"source"`
	Domain      string    `json:"domain,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type WriteInput struct {
	ID             string
	Revision       int64
	Name           string
	Source         string
	Domain         string
	CertificatePEM string
	PrivateKeyPEM  string
}

type SelfSignedInput struct {
	Name       string
	ServerName string
}

type RegenerateInput struct {
	ID         string
	Revision   int64
	ServerName string
}

// AcmeOptions carries the ACME settings needed to issue or renew a certificate.
// It is resolved from the live runtime config at call time so config.yaml edits
// take effect without a restart.
type AcmeOptions struct {
	Email    string
	Provider string
	APIToken string
}

type AcmeInput struct {
	Name   string
	Domain string
}

type Material struct {
	CertificatePEM string
	PrivateKeyPEM  string
}
