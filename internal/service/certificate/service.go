package certificate

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

const privateKeyName = "certificate-private-key-aes-256"

var hostnamePattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Service struct {
	store   *Store
	secrets *secret.Store
	now     func() time.Time
}

func NewService(store *Store, secrets *secret.Store) *Service {
	return &Service{store: store, secrets: secrets, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Create(ctx context.Context, input WriteInput) (Certificate, error) {
	value, encrypted, fields := s.prepare(ctx, input)
	if len(fields) > 0 {
		return Certificate{}, &common.ValidationError{Fields: fields}
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.Create(ctx, record{Certificate: value, CertificatePEM: strings.TrimSpace(input.CertificatePEM), PrivateKeyEncrypted: encrypted}); err != nil {
		return Certificate{}, err
	}
	return value, nil
}

func (s *Service) CreateSelfSigned(ctx context.Context, input SelfSignedInput) (Certificate, error) {
	certificatePEM, privateKeyPEM, err := s.selfSignedMaterial(input.ServerName)
	if err != nil {
		return Certificate{}, err
	}
	return s.Create(ctx, WriteInput{Name: input.Name, Source: SourceSelfSigned, CertificatePEM: certificatePEM, PrivateKeyPEM: privateKeyPEM})
}

func (s *Service) RegenerateSelfSigned(ctx context.Context, input RegenerateInput) (Certificate, error) {
	if input.Revision < 1 {
		return Certificate{}, &common.ValidationError{Fields: map[string]string{"revision": "must be positive"}}
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Certificate{}, err
	}
	certificatePEM, privateKeyPEM, err := s.selfSignedMaterial(input.ServerName)
	if err != nil {
		return Certificate{}, err
	}
	return s.Update(ctx, WriteInput{ID: input.ID, Revision: input.Revision, Name: current.Name, CertificatePEM: certificatePEM, PrivateKeyPEM: privateKeyPEM})
}

func (s *Service) selfSignedMaterial(serverName string) (string, string, error) {
	serverName = strings.TrimSpace(serverName)
	address, parseErr := netip.ParseAddr(serverName)
	if serverName == "" || (parseErr != nil && !hostnamePattern.MatchString(serverName)) {
		fields := map[string]string{"serverName": "must be an IP address or hostname"}
		if serverName == "" {
			fields["serverName"] = "is required"
		}
		return "", "", &common.ValidationError{Fields: fields}
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	now := s.now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: serverName},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if parseErr == nil {
		template.IPAddresses = []net.IP{net.IP(address.AsSlice())}
	} else {
		template.DNSNames = []string{serverName}
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", err
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})), nil
}

func (s *Service) List(ctx context.Context) ([]Certificate, error) { return s.store.List(ctx) }

func (s *Service) Get(ctx context.Context, id string) (Certificate, error) {
	value, err := s.store.Get(ctx, id)
	return value.Certificate, err
}

func (s *Service) Update(ctx context.Context, input WriteInput) (Certificate, error) {
	if input.Revision < 1 {
		return Certificate{}, &common.ValidationError{Fields: map[string]string{"revision": "must be positive"}}
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Certificate{}, err
	}
	if strings.TrimSpace(input.Source) == "" {
		input.Source = current.Source
	}
	value, encrypted, fields := s.prepare(ctx, input)
	if len(fields) > 0 {
		return Certificate{}, &common.ValidationError{Fields: fields}
	}
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = current.ID, current.Revision, current.CreatedAt, s.now()
	if err := s.store.Update(ctx, record{Certificate: value, CertificatePEM: strings.TrimSpace(input.CertificatePEM), PrivateKeyEncrypted: encrypted}, input.Revision); err != nil {
		return Certificate{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.store.Delete(ctx, id) }

func (s *Service) Material(ctx context.Context, id string) (Material, error) {
	value, err := s.store.Get(ctx, id)
	if err != nil {
		return Material{}, err
	}
	plaintext, err := s.secrets.Decrypt(ctx, privateKeyName, value.PrivateKeyEncrypted)
	if err != nil {
		return Material{}, err
	}
	return Material{CertificatePEM: value.CertificatePEM, PrivateKeyPEM: string(plaintext)}, nil
}

func (s *Service) prepare(ctx context.Context, input WriteInput) (Certificate, []byte, map[string]string) {
	fields := map[string]string{}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		fields["name"] = "is required"
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = SourceManual
	}
	certificate, signer, parseFields := parseMaterial(input.CertificatePEM, input.PrivateKeyPEM)
	for key, value := range parseFields {
		fields[key] = value
	}
	if len(fields) > 0 {
		return Certificate{Name: name, Source: source, Domain: strings.TrimSpace(input.Domain)}, nil, fields
	}
	certificateKey, _ := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	privateKey, _ := x509.MarshalPKIXPublicKey(signer.Public())
	if !bytes.Equal(certificateKey, privateKey) {
		fields["privateKeyPem"] = "does not match the certificate"
		return Certificate{Name: name, Source: source, Domain: strings.TrimSpace(input.Domain)}, nil, fields
	}
	encrypted, err := s.secrets.Encrypt(ctx, privateKeyName, []byte(strings.TrimSpace(input.PrivateKeyPEM)))
	if err != nil {
		fields["privateKeyPem"] = "could not be encrypted"
		return Certificate{Name: name, Source: source, Domain: strings.TrimSpace(input.Domain)}, nil, fields
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	return Certificate{Name: name, Source: source, Domain: strings.TrimSpace(input.Domain), Fingerprint: hex.EncodeToString(fingerprint[:]), NotBefore: certificate.NotBefore.UTC(), NotAfter: certificate.NotAfter.UTC()}, encrypted, fields
}

func parseMaterial(certificatePEM, privateKeyPEM string) (*x509.Certificate, crypto.Signer, map[string]string) {
	fields := map[string]string{}
	certificateBlock, _ := pem.Decode([]byte(strings.TrimSpace(certificatePEM)))
	if certificateBlock == nil || certificateBlock.Type != "CERTIFICATE" {
		fields["certificatePem"] = "must contain an X.509 certificate"
	}
	keyBlock, _ := pem.Decode([]byte(strings.TrimSpace(privateKeyPEM)))
	if keyBlock == nil {
		fields["privateKeyPem"] = "must contain a private key"
	}
	if len(fields) > 0 {
		return nil, nil, fields
	}
	certificate, err := x509.ParseCertificate(certificateBlock.Bytes)
	if err != nil {
		fields["certificatePem"] = "contains an invalid certificate"
		return nil, nil, fields
	}
	var key any
	if parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err == nil {
		key = parsed
	} else if parsed, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); err == nil {
		key = parsed
	} else if parsed, err := x509.ParseECPrivateKey(keyBlock.Bytes); err == nil {
		key = parsed
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		fields["privateKeyPem"] = "contains an unsupported private key"
		return nil, nil, fields
	}
	switch signer.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey:
	default:
		// Ed25519 and other crypto.Signer implementations are supported by X.509.
	}
	return certificate, signer, fields
}
