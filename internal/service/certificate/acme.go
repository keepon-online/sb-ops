package certificate

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
	"golang.org/x/crypto/acme"
)

// acmeDirectoryURL is the ACME directory used for issuance. Tests may swap it
// for a staging/Pebble endpoint.
var acmeDirectoryURL = acme.LetsEncryptURL

// IssueACME obtains a trusted certificate for the given domain via DNS-01 and
// stores it with source "acme". It requires acme.email and acme.api_token to
// be configured. A fresh ACME account key is generated per issuance: the
// personal deployment volume is far below Let's Encrypt's per-IP account rate
// limit, so persisting the key is not worth the extra secret plumbing.
func (s *Service) IssueACME(ctx context.Context, input AcmeInput, options AcmeOptions) (Certificate, error) {
	domain := strings.ToLower(strings.TrimSpace(input.Domain))
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Certificate{}, &common.ValidationError{Fields: map[string]string{"name": "is required"}}
	}
	if !hostnamePattern.MatchString(domain) {
		return Certificate{}, &common.ValidationError{Fields: map[string]string{"domain": "must be a valid domain name"}}
	}
	if strings.TrimSpace(options.Email) == "" || strings.TrimSpace(options.APIToken) == "" {
		return Certificate{}, fmt.Errorf("ACME 未配置：请在 config.yaml 的 acme 段填写 email 与 api_token")
	}

	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, err
	}
	client := &acme.Client{Key: accountKey, DirectoryURL: acmeDirectoryURL}
	if _, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + strings.TrimSpace(options.Email)}}, acme.AcceptTOS); err != nil {
		return Certificate{}, fmt.Errorf("注册 ACME 账户: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return Certificate{}, fmt.Errorf("创建 ACME 订单: %w", err)
	}
	dns := newCloudflareDNS(strings.TrimSpace(options.APIToken))
	for _, authzURL := range order.AuthzURLs {
		if err := s.completeDNS01(ctx, client, dns, authzURL, domain); err != nil {
			return Certificate{}, err
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, key)
	if err != nil {
		return Certificate{}, err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return Certificate{}, fmt.Errorf("签发证书: %w", err)
	}
	var certPEM bytes.Buffer
	for _, der := range chain {
		certPEM.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return s.Create(ctx, WriteInput{Name: name, Source: SourceACME, Domain: domain, CertificatePEM: certPEM.String(), PrivateKeyPEM: string(keyPEM)})
}

// RenewACME re-issues the certificate for an existing ACME certificate using
// its stored domain.
func (s *Service) RenewACME(ctx context.Context, id string, options AcmeOptions) (Certificate, error) {
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return Certificate{}, err
	}
	if current.Source != SourceACME || current.Domain == "" {
		return Certificate{}, fmt.Errorf("证书 %s 不是 ACME 证书", current.Name)
	}
	return s.IssueACME(ctx, AcmeInput{Name: current.Name, Domain: current.Domain}, options)
}

// RenewDueACME renews ACME certificates whose not_after falls within the next
// renewalWindow, returning how many were renewed. When ACME is not configured
// it is a no-op.
func (s *Service) RenewDueACME(ctx context.Context, options AcmeOptions, renewalWindow time.Duration) (int, error) {
	if strings.TrimSpace(options.Email) == "" || strings.TrimSpace(options.APIToken) == "" {
		return 0, nil
	}
	certs, err := s.store.List(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(renewalWindow)
	renewed := 0
	for _, cert := range certs {
		if cert.Source != SourceACME || cert.Domain == "" {
			continue
		}
		if cert.NotAfter.After(cutoff) {
			continue
		}
		if _, err := s.RenewACME(ctx, cert.ID, options); err != nil {
			return renewed, err
		}
		renewed++
	}
	return renewed, nil
}

func (s *Service) completeDNS01(ctx context.Context, client *acme.Client, dns *cloudflareDNS, authzURL, domain string) error {
	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return err
	}
	if authz.Status == acme.StatusValid {
		return nil
	}
	var chal *acme.Challenge
	for _, candidate := range authz.Challenges {
		if candidate.Type == "dns-01" {
			chal = candidate
			break
		}
	}
	if chal == nil {
		return fmt.Errorf("ACME 未提供 dns-01 挑战")
	}
	record, err := client.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return err
	}
	fqdn := "_acme-challenge." + strings.TrimPrefix(domain, "*.")
	if err := dns.present(ctx, fqdn, record); err != nil {
		return fmt.Errorf("写入 DNS 记录: %w", err)
	}
	defer func() { _ = dns.cleanup(context.Background(), fqdn, record) }()
	if _, err := client.Accept(ctx, chal); err != nil {
		return fmt.Errorf("提交 ACME 挑战: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authz.URI); err != nil {
		return fmt.Errorf("等待 ACME 验证: %w", err)
	}
	return nil
}

const cloudflareBaseURL = "https://api.cloudflare.com/client/v4"

type cloudflareDNS struct {
	token  string
	client *http.Client
}

func newCloudflareDNS(token string) *cloudflareDNS {
	return &cloudflareDNS{token: token, client: &http.Client{Timeout: 20 * time.Second}}
}

type cloudflareZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cloudflareRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (c *cloudflareDNS) present(ctx context.Context, fqdn, value string) error {
	domain := strings.TrimPrefix(fqdn, "_acme-challenge.")
	zoneID, zoneName, err := c.findZone(ctx, domain)
	if err != nil {
		return err
	}
	name := relativeName(fqdn, zoneName)
	return c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), map[string]any{
		"type": "TXT", "name": name, "content": value, "ttl": 120,
	}, nil)
}

func (c *cloudflareDNS) cleanup(ctx context.Context, fqdn, value string) error {
	domain := strings.TrimPrefix(fqdn, "_acme-challenge.")
	zoneID, zoneName, err := c.findZone(ctx, domain)
	if err != nil {
		return err
	}
	name := relativeName(fqdn, zoneName)
	var records []cloudflareRecord
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?type=TXT&name=%s", zoneID, name), nil, &records); err != nil {
		return err
	}
	for _, record := range records {
		if record.Content != value {
			continue
		}
		if err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, record.ID), nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (c *cloudflareDNS) findZone(ctx context.Context, domain string) (id, name string, err error) {
	labels := strings.Split(strings.TrimSuffix(domain, "."), ".")
	for i := range labels {
		candidate := strings.Join(labels[i:], ".")
		var zones []cloudflareZone
		if err := c.do(ctx, http.MethodGet, "/zones?name="+candidate, nil, &zones); err != nil {
			return "", "", err
		}
		if len(zones) > 0 {
			return zones[0].ID, zones[0].Name, nil
		}
	}
	return "", "", fmt.Errorf("未找到域名 %s 的 Cloudflare 区域", domain)
}

func (c *cloudflareDNS) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, cloudflareBaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("解析 Cloudflare 响应: %w", err)
	}
	if !envelope.Success {
		if len(envelope.Errors) > 0 {
			return fmt.Errorf("请求 Cloudflare API 失败：%s", envelope.Errors[0].Message)
		}
		return fmt.Errorf("cloudflare API 请求失败")
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return err
		}
	}
	return nil
}

func relativeName(fqdn, zoneName string) string {
	fqdn = strings.TrimSuffix(fqdn, ".")
	zoneName = strings.TrimSuffix(zoneName, ".")
	if fqdn == zoneName {
		return fqdn
	}
	if strings.HasSuffix(fqdn, "."+zoneName) {
		return strings.TrimSuffix(fqdn, "."+zoneName)
	}
	return fqdn
}
