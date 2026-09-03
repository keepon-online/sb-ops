package probe

import (
	"fmt"
	"net"
	"strings"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

// probeServer 是拨测客户端出站的拨号目标：协议入站监听在（默认 ::）本机，
// 从环回连入即穿过与真实客户端完全相同的入站链路。
const probeServer = "127.0.0.1"

// clientOutbound 生成拨测用的 sing-box 客户端出站，字段语义镜像服务端
// inbound builder（internal/deployment/compiler）——探针以服务端入站为准，
// 而非订阅方言（如 VMess 订阅恒不加密，服务端 certificate 模式则必须 TLS）。
func clientOutbound(protocol serviceprotocol.Protocol, device serviceuser.ArtifactDevice) (map[string]any, error) {
	value := map[string]any{
		"tag": "probe-out", "server": probeServer, "server_port": protocol.ListenPort,
	}
	switch protocol.Type {
	case serviceprotocol.TypeVLESS:
		value["type"] = "vless"
		value["uuid"] = device.UUID
		if isTCPTransport(protocol) {
			value["flow"] = "xtls-rprx-vision"
		}
		if transport := clientTransport(protocol); transport != nil {
			value["transport"] = transport
		}
		value["tls"] = tlsOrReality(protocol)
	case serviceprotocol.TypeVMess:
		value["type"] = "vmess"
		value["uuid"] = device.UUID
		value["security"] = "auto"
		value["alter_id"] = 0
		value["transport"] = map[string]any{
			"type":                   "ws",
			"path":                   setting(protocol, "wsPath"),
			"max_early_data":         numberSetting(protocol, "wsMaxEarlyData", 0),
			"early_data_header_name": setting(protocol, "wsEarlyDataHeaderName"),
		}
		if setting(protocol, "tlsMode") == "certificate" {
			value["tls"] = certificateTLS(protocol)
		}
	case serviceprotocol.TypeHysteria2:
		value["type"] = "hysteria2"
		value["password"] = device.Password
		value["tls"] = certificateTLS(protocol)
		value["tls"].(map[string]any)["alpn"] = []string{"h3"}
	case serviceprotocol.TypeTUIC:
		value["type"] = "tuic"
		value["uuid"] = device.UUID
		value["password"] = device.Password
		value["congestion_control"] = setting(protocol, "congestionControl")
		value["udp_relay_mode"] = "native"
		value["tls"] = certificateTLS(protocol)
		value["tls"].(map[string]any)["alpn"] = []string{"h3"}
	case serviceprotocol.TypeAnyTLS:
		value["type"] = "anytls"
		value["password"] = device.Password
		value["tls"] = certificateTLS(protocol)
	case serviceprotocol.TypeShadowsocks:
		value["type"] = "shadowsocks"
		value["method"] = setting(protocol, "method")
		if strings.HasPrefix(setting(protocol, "method"), "2022-") {
			value["password"] = setting(protocol, "password") + ":" + serviceprotocol.ShadowsocksUserPassword(setting(protocol, "method"), device.Password)
		} else {
			value["password"] = setting(protocol, "password")
		}
	case serviceprotocol.TypeTrojan:
		value["type"] = "trojan"
		value["password"] = device.Password
		value["tls"] = tlsOrReality(protocol)
	default:
		return nil, fmt.Errorf("协议类型 %s 不支持拨测", protocol.Type)
	}
	return value, nil
}

// certificateTLS 是证书模式（含 Hysteria2/TUIC/AnyTLS 恒 TLS）的客户端 TLS 段。
// insecure 复刻订阅语义：serverName 是 IP（自签）则跳过校验，是域名（ACME
// 可信）则真实验证——保留证书过期检测。
func certificateTLS(protocol serviceprotocol.Protocol) map[string]any {
	serverName := setting(protocol, "serverName")
	return map[string]any{
		"enabled": true, "server_name": serverName, "insecure": net.ParseIP(serverName) != nil,
	}
}

// tlsOrReality 镜像服务端 tlsOrReality：Reality 用 public_key + short_id
// （Reality 客户端必须带 uTLS 指纹），证书模式退化为常规 TLS。
func tlsOrReality(protocol serviceprotocol.Protocol) map[string]any {
	if setting(protocol, "tlsMode") != "reality" {
		return certificateTLS(protocol)
	}
	return map[string]any{
		"enabled": true, "server_name": setting(protocol, "serverName"),
		"utls":    map[string]any{"enabled": true, "fingerprint": "chrome"},
		"reality": map[string]any{"enabled": true, "public_key": setting(protocol, "realityPublicKey"), "short_id": setting(protocol, "shortId")},
	}
}

func clientTransport(protocol serviceprotocol.Protocol) map[string]any {
	switch setting(protocol, "transport") {
	case "ws":
		return map[string]any{"type": "ws", "path": setting(protocol, "wsPath")}
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": setting(protocol, "grpcServiceName")}
	case "httpupgrade":
		return map[string]any{"type": "httpupgrade", "path": setting(protocol, "httpupgradePath")}
	case "http":
		return map[string]any{"type": "http", "path": setting(protocol, "httpPath")}
	default:
		return nil // tcp 是隐式默认
	}
}

// isTCPTransport 与服务端同判：仅 TCP 传输给 Vision flow。
func isTCPTransport(protocol serviceprotocol.Protocol) bool {
	switch setting(protocol, "transport") {
	case "", "<nil>", "tcp":
		return true
	default:
		return false
	}
}

func setting(value serviceprotocol.Protocol, key string) string {
	if value.Settings[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value.Settings[key]))
}

func numberSetting(value serviceprotocol.Protocol, key string, fallback int) int {
	switch number := value.Settings[key].(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return fallback
	}
}
