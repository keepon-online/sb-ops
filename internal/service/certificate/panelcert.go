package certificate

import "context"

// PanelCertificateName 是面板自用 TLS 证书在 certificates 表中的固定
// 名称（server.tls_listen 启用时由装配层查找/签发）。
const PanelCertificateName = "panel-tls"

// EnsurePanelCertificate 返回面板 TLS 直听可用的 ACME 证书：表中已有该
// 固定名称的证书直接复用（续期由既有 6h 循环按窗口覆盖），缺失则立即
// DNS-01 签发一张——首次启用 server.tls_listen 时启动会阻塞数十秒属
// 预期行为。证书不落盘：Material() 输出 PEM 直接构建 tls.Certificate。
func (s *Service) EnsurePanelCertificate(ctx context.Context, domain string, options AcmeOptions) (Certificate, error) {
	values, err := s.List(ctx)
	if err != nil {
		return Certificate{}, err
	}
	for _, value := range values {
		if value.Name == PanelCertificateName {
			return value, nil
		}
	}
	return s.IssueACME(ctx, AcmeInput{Name: PanelCertificateName, Domain: domain}, options)
}
