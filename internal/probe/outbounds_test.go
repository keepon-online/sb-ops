package probe

import (
	"encoding/base64"
	"testing"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

var probeDevice = serviceuser.ArtifactDevice{UserID: "user-1", DeviceID: "device-1", Enabled: true, UUID: "11111111-2222-4333-8444-555555555555", Password: "device-password"}

func outboundFor(t *testing.T, protocolType serviceprotocol.Type, settings map[string]any) map[string]any {
	t.Helper()
	outbound, err := clientOutbound(serviceprotocol.Protocol{
		ID: "p1", Name: "协议一", Type: protocolType, Enabled: true, Listen: "::", ListenPort: 18443,
		Settings: settings,
	}, probeDevice)
	if err != nil {
		t.Fatalf("clientOutbound(%s): %v", protocolType, err)
	}
	return outbound
}

func TestVLESSRealityTCPOutbound(t *testing.T) {
	outbound := outboundFor(t, serviceprotocol.TypeVLESS, map[string]any{
		"transport": "tcp", "tlsMode": "reality", "serverName": "www.apple.com",
		"realityPublicKey": "pub-key", "shortId": "abcd",
	})
	if outbound["flow"] != "xtls-rprx-vision" {
		t.Fatalf("flow=%v, want xtls-rprx-vision（tcp 传输带 Vision）", outbound["flow"])
	}
	if outbound["server"] != probeServer || outbound["server_port"] != 18443 {
		t.Fatalf("拨号目标 %v:%v, want %s:18443（环回穿真实入站）", outbound["server"], outbound["server_port"], probeServer)
	}
	tls := outbound["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != "pub-key" || reality["short_id"] != "abcd" {
		t.Fatalf("reality 参数镜像错误: %v", reality)
	}
	if _, ok := tls["utls"]; !ok {
		t.Fatal("reality 客户端必须带 utls 指纹")
	}
	if _, ok := tls["insecure"]; ok {
		t.Fatal("reality 模式不应设置 insecure")
	}
}

func TestVLESSCertificateTransportOutbound(t *testing.T) {
	for transport, expect := range map[string]map[string]any{
		"ws":          {"type": "ws", "path": "/ws"},
		"grpc":        {"type": "grpc", "service_name": "svc"},
		"httpupgrade": {"type": "httpupgrade", "path": "/up"},
		"http":        {"type": "http", "path": "/h"},
	} {
		settings := map[string]any{"transport": transport, "tlsMode": "certificate", "serverName": "203.0.113.10"}
		for key, value := range map[string]string{
			"wsPath": "/ws", "grpcServiceName": "svc", "httpupgradePath": "/up", "httpPath": "/h",
		} {
			settings[key] = value
		}
		outbound := outboundFor(t, serviceprotocol.TypeVLESS, settings)
		if _, hasFlow := outbound["flow"]; hasFlow {
			t.Fatalf("transport=%s 不应带 Vision flow", transport)
		}
		got := outbound["transport"].(map[string]any)
		for key, value := range expect {
			if got[key] != value {
				t.Fatalf("transport=%s %s=%v, want %v", transport, key, got[key], value)
			}
		}
		tls := outbound["tls"].(map[string]any)
		// serverName 是 IP（自签证书）→ 跳过校验；域名（ACME）则保持真实验证。
		if tls["insecure"] != true {
			t.Fatalf("transport=%s IP serverName 应 insecure=true", transport)
		}
	}
}

func TestVLESSDomainServerNameVerifiesCertificate(t *testing.T) {
	outbound := outboundFor(t, serviceprotocol.TypeVLESS, map[string]any{
		"transport": "tcp", "tlsMode": "certificate", "serverName": "vpn.example.com",
	})
	tls := outbound["tls"].(map[string]any)
	if tls["insecure"] != false {
		t.Fatal("域名 serverName（ACME 证书）应真实验证以捕捉证书过期")
	}
}

func TestVMessMirrorsServerTLS(t *testing.T) {
	outbound := outboundFor(t, serviceprotocol.TypeVMess, map[string]any{
		"transport": "ws", "wsPath": "/vm", "wsMaxEarlyData": 2048, "wsEarlyDataHeaderName": "Sec-WebSocket-Protocol",
	})
	if _, hasTLS := outbound["tls"]; hasTLS {
		t.Fatal("tlsMode=none 的服务端无 TLS，客户端不应带 tls 段（比订阅方言更贴近服务端）")
	}
	transport := outbound["transport"].(map[string]any)
	if transport["type"] != "ws" || transport["path"] != "/vm" || transport["max_early_data"] != 2048 {
		t.Fatalf("ws transport 镜像错误: %v", transport)
	}
	if transport["early_data_header_name"] != "Sec-WebSocket-Protocol" {
		t.Fatalf("early data header 镜像错误: %v", transport)
	}

	secure := outboundFor(t, serviceprotocol.TypeVMess, map[string]any{
		"tlsMode": "certificate", "serverName": "203.0.113.10", "wsPath": "/vm",
	})
	if _, hasTLS := secure["tls"]; !hasTLS {
		t.Fatal("tlsMode=certificate 的服务端有 TLS，客户端必须镜像")
	}
}

func TestUDPProtocolsMirrorTLS(t *testing.T) {
	hy2 := outboundFor(t, serviceprotocol.TypeHysteria2, map[string]any{"serverName": "203.0.113.10", "password": "x"})
	if hy2["password"] != "device-password" {
		t.Fatalf("hysteria2 password 应为设备凭据: %v", hy2["password"])
	}
	tls := hy2["tls"].(map[string]any)
	if tls["insecure"] != true || tls["alpn"].([]string)[0] != "h3" {
		t.Fatalf("hysteria2 tls 镜像错误: %v", tls)
	}

	tuic := outboundFor(t, serviceprotocol.TypeTUIC, map[string]any{"serverName": "203.0.113.10", "congestionControl": "bbr"})
	if tuic["uuid"] != probeDevice.UUID || tuic["congestion_control"] != "bbr" || tuic["udp_relay_mode"] != "native" {
		t.Fatalf("tuic 镜像错误: %v", tuic)
	}

	anytls := outboundFor(t, serviceprotocol.TypeAnyTLS, map[string]any{"serverName": "203.0.113.10"})
	if anytls["type"] != "anytls" || anytls["password"] != "device-password" {
		t.Fatalf("anytls 镜像错误: %v", anytls)
	}
}

func TestShadowsocksCompositePassword(t *testing.T) {
	serverKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	outbound := outboundFor(t, serviceprotocol.TypeShadowsocks, map[string]any{
		"method": "2022-blake3-aes-128-gcm", "password": serverKey,
	})
	// SS-2022 多用户：客户端密码必须是「服务端密钥:用户密钥」组合。
	want := serverKey + ":" + serviceprotocol.ShadowsocksUserPassword("2022-blake3-aes-128-gcm", probeDevice.Password)
	if outbound["password"] != want {
		t.Fatalf("password=%v, want 复合密码", outbound["password"])
	}
}

func TestTrojanRealityOutbound(t *testing.T) {
	outbound := outboundFor(t, serviceprotocol.TypeTrojan, map[string]any{
		"tlsMode": "reality", "serverName": "gateway.icloud.com",
		"realityPublicKey": "pub", "shortId": "ef01",
	})
	if outbound["password"] != "device-password" {
		t.Fatalf("trojan password 应为设备凭据: %v", outbound["password"])
	}
	tls := outbound["tls"].(map[string]any)
	if _, ok := tls["reality"]; !ok {
		t.Fatal("trojan reality 模式应带 reality 段")
	}
}

func TestUnsupportedProtocolTypeFails(t *testing.T) {
	if _, err := clientOutbound(serviceprotocol.Protocol{
		Type: serviceprotocol.Type("naive"), Settings: map[string]any{},
	}, probeDevice); err == nil {
		t.Fatal("不支持的协议类型应返回错误")
	}
}
