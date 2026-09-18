package compiler

import (
	"os"
	"testing"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

// TestDumpServerConfigForExternalCheck 供外部 `sing-box check` 字段核验：
// SB_CHECK_DUMP=<path> go test -run TestDumpServerConfigForExternalCheck ./internal/deployment/compiler
// 产出覆盖全协议入站、WARP endpoint、路由规则/规则集、typed DNS 与 experimental 段。
func TestDumpServerConfigForExternalCheck(t *testing.T) {
	dump := os.Getenv("SB_CHECK_DUMP")
	if dump == "" {
		t.Skip("set SB_CHECK_DUMP=<path> to dump the server config")
	}
	protocols := []serviceprotocol.Protocol{
		{ID: "vless", Name: "VLESS", Type: serviceprotocol.TypeVLESS, Enabled: true, Listen: "::", ListenPort: 2443, Settings: map[string]any{"tlsMode": "reality", "serverName": "www.apple.com", "realityPrivateKey": "yik4VXbFQIL0HzBC2YaTHM0681f4f__GmIqCNY35Cd0", "realityPublicKey": "public", "shortId": "01234567"}},
		{ID: "vmess", Name: "VMess WS", Type: serviceprotocol.TypeVMess, Enabled: true, Listen: "::", ListenPort: 2445, Settings: map[string]any{"transport": "ws", "wsPath": "/vm", "wsMaxEarlyData": 2048, "wsEarlyDataHeaderName": "Sec-WebSocket-Protocol"}},
		{ID: "hy2", Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, Listen: "::", ListenPort: 2446, Settings: map[string]any{"serverName": "www.bing.com", "upMbps": 100, "downMbps": 100}},
		{ID: "tuic", Name: "TUIC", Type: serviceprotocol.TypeTUIC, Enabled: true, Listen: "::", ListenPort: 2447, Settings: map[string]any{"serverName": "www.bing.com", "congestionControl": "bbr"}},
		{ID: "anytls", Name: "AnyTLS", Type: serviceprotocol.TypeAnyTLS, Enabled: true, Listen: "::", ListenPort: 2448, Settings: map[string]any{"serverName": "www.bing.com"}},
		{ID: "ss", Name: "SS-2022", Type: serviceprotocol.TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444, Settings: map[string]any{"method": "2022-blake3-aes-256-gcm", "password": "d32qMUSzUz9yW8x6kVECeynNN3XsTKSjXwB4BUHaKcs="}},
		{ID: "trojan", Name: "Trojan", Type: serviceprotocol.TypeTrojan, Enabled: true, Listen: "::", ListenPort: 2449, Settings: map[string]any{"tlsMode": "certificate", "serverName": "panel.example.com"}},
	}
	user := serviceuser.ArtifactUser{ID: "user", Name: "checker", Enabled: true}
	device := testDevice("user", "checker-password")
	assignments := map[string]map[string]bool{"user": {"vless": true, "vmess": true, "hy2": true, "tuic": true, "anytls": true, "ss": true, "trojan": true}}
	result, err := Compile(Input{
		PublicAddress: "203.0.113.10", TLSCertPath: "/tmp/sb-check/fullchain.pem", TLSKeyPath: "/tmp/sb-check/privkey.pem",
		Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments,
		SingBoxConfig: servicesingbox.Config{
			LogLevel: "info", LogTimestamp: true,
			DNSServers: []servicesingbox.DNSServer{
				{Tag: "bootstrap", Address: "udp://223.5.5.5"},
				{Tag: "remote", Address: "https://dns.google/dns-query", Detour: "warp"},
			}, DNSFinal: "remote",
			ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", V2RayAPIEnabled: true, V2RayAPIListen: "127.0.0.1:10000", DefaultOutboundTag: "direct",
		},
		ClashAPISecret: "check-secret",
		Outbounds: []servicesingbox.Outbound{
			{ID: "proxy", Tag: "proxy", Type: "socks", Server: "127.0.0.1", ServerPort: 1080, Username: "user"},
			{ID: "warp", Tag: "warp", Type: "warp", Warp: &servicesingbox.WarpSettings{Endpoint: "engage.cloudflareclient.com:2408", PublicKey: "3tl5uEEOhjyjSixpt55tvzbM/D6oO7kJG3ch0OfXzgk=", PrivateKey: "9PRIVf7JLyJ75w6bAbi99HNQcKoqQzt0VNWHT0uVsH0=", IPv4: "172.16.0.2/32", Reserved: []int{1, 2, 3}, MTU: 1408}},
		},
		OutboundPasswords: map[string]string{"proxy": "upstream"},
		RouteRules: []servicesingbox.RouteRule{
			{Name: "domains", Enabled: true, Priority: 10, OutboundTag: "proxy", Conditions: servicesingbox.RouteConditions{Domains: []string{"example.com"}, DomainSuffixes: []string{"example.org"}}},
			{Name: "cidr", Enabled: true, Priority: 20, OutboundTag: "direct", Conditions: servicesingbox.RouteConditions{IPCIDRs: []string{"10.0.0.0/8"}, Ports: []int{443}, Networks: []string{"udp"}, InboundTags: []string{"hy2-in"}}},
			{Name: "ruleset", Enabled: true, Priority: 30, OutboundTag: "warp", Conditions: servicesingbox.RouteConditions{RuleSets: []string{"ads"}}},
		},
		RuleSets:      []servicesingbox.RuleSet{{Tag: "ads", Format: "binary", URL: "https://example.com/ads.srs", DownloadDetour: "direct"}},
		CacheFilePath: "/tmp/sb-check/cache.db",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := os.WriteFile(dump, result.ServerConfig, 0o600); err != nil {
		t.Fatalf("write dump: %v", err)
	}
}
