package protocol

import "maps"

import "slices"

var schemas = map[Type]Schema{
	TypeVLESS: {
		Type:                    TypeVLESS,
		CertificateRequiredWhen: map[string][]string{"tlsMode": {"certificate"}},
		Fields: []Field{
			{Name: "transport", Label: "传输方式", Kind: "select", Group: "transport", Required: true, Options: []string{"tcp", "ws", "grpc", "httpupgrade", "http"}, Hint: "tcp 为默认；ws/grpc/httpupgrade/http 用于伪装成网页/HTTP 流量"},
			{Name: "wsPath", Label: "WebSocket 路径", Kind: "text", Group: "transport", Hint: "ws 传输的路径"},
			{Name: "grpcServiceName", Label: "gRPC 服务名", Kind: "text", Group: "transport", Hint: "grpc 传输的服务名"},
			{Name: "httpupgradePath", Label: "HTTPUpgrade 路径", Kind: "text", Group: "transport", Hint: "httpupgrade 传输的路径"},
			{Name: "httpPath", Label: "HTTP 路径", Kind: "text", Group: "transport", Hint: "http 传输的路径"},
			{Name: "tlsMode", Label: "TLS 模式", Kind: "select", Group: "tls", Required: true, Options: []string{"reality", "certificate"}, Hint: "Reality 免域名免证书、伪装最强（推荐）；证书模式需选择 TLS 证书"},
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Required: true, Hint: "Reality 模式下是伪装目标站点 SNI；证书模式下是证书域名"},
			{Name: "handshakeServerPort", Label: "Reality 回退端口", Kind: "number", Group: "reality", Hint: "Reality 回退目标端口，默认 443"},
			{Name: "realityPrivateKey", Label: "Reality 私钥", Kind: "password", Group: "reality", Secret: true, Hint: "留空由服务端自动生成"},
			{Name: "realityPublicKey", Label: "Reality 公钥", Kind: "text", Group: "reality", Hint: "自动生成，供客户端配置使用"},
			{Name: "shortId", Label: "Short ID", Kind: "text", Group: "reality", Hint: "留空由服务端自动生成"},
			{Name: "tcpFastOpen", Label: "TCP Fast Open", Kind: "boolean", Group: "advanced", Hint: "启用 TCP Fast Open 快速握手，降低首次建连延迟（默认开启）"},
			{Name: "tcpKeepAlive", Label: "TCP 保活周期", Kind: "text", Group: "advanced", Hint: "TCP Keep-Alive 空闲检测周期（如 15s），快速剔除失效连接"},
		},
		Values: map[string]any{"transport": "tcp", "wsPath": "/", "grpcServiceName": "Tun", "httpupgradePath": "/", "httpPath": "/", "tlsMode": "reality", "serverName": "www.apple.com", "handshakeServerPort": 443, "tcpFastOpen": true, "tcpKeepAlive": "15s"},
	},
	TypeVMess: {
		Type:                    TypeVMess,
		CertificateRequiredWhen: map[string][]string{"tlsMode": {"certificate"}},
		Fields: []Field{
			{Name: "transport", Label: "传输方式", Kind: "select", Group: "transport", Required: true, Options: []string{"ws"}, Hint: "WebSocket 伪装"},
			{Name: "wsPath", Label: "WebSocket 路径", Kind: "text", Group: "transport", Required: true, Hint: "WebSocket 的路径"},
			{Name: "wsMaxEarlyData", Label: "WebSocket Early Data", Kind: "number", Group: "transport", Required: true, Hint: "0 表示关闭 Early Data"},
			{Name: "wsEarlyDataHeaderName", Label: "Early Data 请求头", Kind: "text", Group: "transport", Required: true},
			{Name: "tlsMode", Label: "TLS 模式", Kind: "select", Group: "tls", Required: true, Options: []string{"none", "certificate"}, Hint: "无 TLS 或证书模式"},
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Hint: "证书域名（仅证书模式）"},
		},
		Values: map[string]any{"transport": "ws", "wsPath": "/", "wsMaxEarlyData": 0, "wsEarlyDataHeaderName": "Sec-WebSocket-Protocol", "tlsMode": "none"},
	},
	TypeHysteria2: {
		Type:                TypeHysteria2,
		CertificateRequired: true,
		Fields: []Field{
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Required: true, Hint: "证书域名，需与所选证书匹配"},
			{Name: "upMbps", Label: "上行 Mbps", Kind: "number", Group: "advanced", Required: true, Hint: "给客户端的带宽上限"},
			{Name: "downMbps", Label: "下行 Mbps", Kind: "number", Group: "advanced", Required: true, Hint: "给客户端的带宽上限"},
			{Name: "ignoreClientBandwidth", Label: "禁用客户端 BBR", Kind: "boolean", Group: "advanced", Hint: "强制要求客户端使用固定速率 (Brutal)，禁止客户端切换为 BBR 拥塞控制（默认关闭）"},
			{Name: "portHopFrom", Label: "跳跃起始端口", Kind: "number", Group: "advanced", Hint: "端口跳跃起始端口，留空关闭；需 root + iptables"},
			{Name: "portHopTo", Label: "跳跃结束端口", Kind: "number", Group: "advanced", Hint: "端口跳跃结束端口，与起始端口成对使用"},
			{Name: "hopInterval", Label: "跳跃间隔 (秒)", Kind: "number", Group: "advanced", Hint: "客户端端口跳跃间隔，默认 30 秒"},
			{Name: "password", Label: "默认密码", Kind: "password", Group: "security", Secret: true, Hint: "留空由服务端自动生成"},
		},
		Values: map[string]any{"upMbps": 100, "downMbps": 100, "hopInterval": 30},
	},
	TypeTUIC: {
		Type:                TypeTUIC,
		CertificateRequired: true,
		Fields: []Field{
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Required: true, Hint: "证书域名，需与所选证书匹配"},
			{Name: "congestionControl", Label: "拥塞控制", Kind: "select", Group: "advanced", Required: true, Options: []string{"bbr", "cubic", "new_reno"}, Hint: "拥塞控制算法，bbr 通用"},
			{Name: "portHopFrom", Label: "跳跃起始端口", Kind: "number", Group: "advanced", Hint: "端口跳跃起始端口，留空关闭；需 root + iptables"},
			{Name: "portHopTo", Label: "跳跃结束端口", Kind: "number", Group: "advanced", Hint: "端口跳跃结束端口，与起始端口成对使用"},
			{Name: "password", Label: "默认密码", Kind: "password", Group: "security", Secret: true, Hint: "留空由服务端自动生成"},
		},
		Values: map[string]any{"congestionControl": "bbr"},
	},
	TypeAnyTLS: {
		Type:                TypeAnyTLS,
		CertificateRequired: true,
		Fields: []Field{
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Required: true, Hint: "证书域名，需与所选证书匹配"},
			{Name: "password", Label: "默认密码", Kind: "password", Group: "security", Secret: true, Hint: "留空由服务端自动生成"},
		},
		Values: map[string]any{},
	},
	TypeShadowsocks: {
		Type: TypeShadowsocks,
		Fields: []Field{
			{Name: "method", Label: "加密方法", Kind: "select", Group: "security", Required: true, Options: []string{"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"}, Hint: "Shadowsocks 2022 加密方法"},
			{Name: "password", Label: "默认密钥", Kind: "password", Group: "security", Secret: true, Hint: "留空由服务端自动生成；2022 方法需 Base64 密钥"},
		},
		Values: map[string]any{"method": "2022-blake3-aes-128-gcm"},
	},
	TypeTrojan: {
		Type:                    TypeTrojan,
		CertificateRequiredWhen: map[string][]string{"tlsMode": {"certificate"}},
		Fields: []Field{
			{Name: "tlsMode", Label: "TLS 模式", Kind: "select", Group: "tls", Required: true, Options: []string{"reality", "certificate"}, Hint: "Reality 免域名免证书（推荐）；证书模式需选择 TLS 证书"},
			{Name: "serverName", Label: "服务器名称", Kind: "text", Group: "tls", Required: true, Hint: "Reality 模式下是伪装目标站点 SNI；证书模式下是证书域名"},
			{Name: "handshakeServerPort", Label: "Reality 回退端口", Kind: "number", Group: "reality", Hint: "Reality 回退目标端口，默认 443"},
			{Name: "realityPrivateKey", Label: "Reality 私钥", Kind: "password", Group: "reality", Secret: true, Hint: "留空由服务端自动生成"},
			{Name: "realityPublicKey", Label: "Reality 公钥", Kind: "text", Group: "reality", Hint: "自动生成，供客户端配置使用"},
			{Name: "shortId", Label: "Short ID", Kind: "text", Group: "reality", Hint: "留空由服务端自动生成"},
			{Name: "tcpFastOpen", Label: "TCP Fast Open", Kind: "boolean", Group: "advanced", Hint: "启用 TCP Fast Open 快速握手，降低首次建连延迟（默认开启）"},
			{Name: "tcpKeepAlive", Label: "TCP 保活周期", Kind: "text", Group: "advanced", Hint: "TCP Keep-Alive 空闲检测周期（如 15s），快速剔除失效连接"},
			{Name: "password", Label: "默认密码", Kind: "password", Group: "security", Secret: true, Hint: "留空由服务端自动生成"},
		},
		Values: map[string]any{"tlsMode": "reality", "serverName": "www.apple.com", "handshakeServerPort": 443, "tcpFastOpen": true, "tcpKeepAlive": "15s"},
	},
}

func SchemaFor(value Type) (Schema, bool) {
	schema, ok := schemas[value]
	if !ok {
		return Schema{}, false
	}
	schema.Values = cloneMap(schema.Values)
	schema.Fields = slices.Clone(schema.Fields)
	if schema.CertificateRequiredWhen != nil {
		// 先保存源 map 再重建，避免 range 遍历到刚替换的空目标 map
		// 导致证书条件丢失（前端因此不渲染 TLS 证书选择器）。
		source := schema.CertificateRequiredWhen
		schema.CertificateRequiredWhen = make(map[string][]string, len(source))
		for field, values := range source {
			schema.CertificateRequiredWhen[field] = slices.Clone(values)
		}
	}
	return schema, true
}

func cloneMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	maps.Copy(result, source)
	return result
}
