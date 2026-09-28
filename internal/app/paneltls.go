package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync/atomic"

	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
)

// panelTLS 缓存面板自用 TLS 证书并支持续期后热载：tls.Config 的
// GetCertificate 回调读这里，避免每次握手都从库里解密私钥。
type panelTLS struct {
	certificate   atomic.Pointer[tls.Certificate]
	service       *servicecertificate.Service
	certificateID string
}

// loadPanelCertificate 从库里解密并解析证书材料，成功后原子替换缓存。
func (p *panelTLS) load(ctx context.Context) error {
	material, err := p.service.Material(ctx, p.certificateID)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair([]byte(material.CertificatePEM), []byte(material.PrivateKeyPEM))
	if err != nil {
		return fmt.Errorf("解析面板 TLS 证书失败: %w", err)
	}
	p.certificate.Store(&pair)
	return nil
}

// config 返回供 443 监听器使用的 tls.Config（证书经缓存回调提供，续期
// 热载后新握手即用新材料；Go 的 ALPN 自动携带 h2）。
func (p *panelTLS) config() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if pair := p.certificate.Load(); pair != nil {
				return pair, nil
			}
			return nil, errors.New("面板 TLS 证书尚未加载")
		},
	}
}

// PanelTLS 返回面板自带 TLS 监听的配置；未启用（server.tls_listen 为空）
// 时为 nil。
func (a *OpsApp) PanelTLS() *tls.Config {
	if a.panelTLS == nil {
		return nil
	}
	return a.panelTLS.config()
}
