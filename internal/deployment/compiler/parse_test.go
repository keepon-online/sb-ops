package compiler

import (
	"context"
	"testing"

	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
)

// TestAllProtocolTypesParseInSingBox compiles every supported protocol type and
// runs the result through sing-box's strict JSON parser, so an unknown-field or
// malformed inbound is caught at test time rather than at runtime sync.
func TestAllProtocolTypesParseInSingBox(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "alice", Enabled: true}
	device := testDevice("user", "pw")
	protocols := []serviceprotocol.Protocol{
		{ID: "vless", Name: "VLESS", Type: serviceprotocol.TypeVLESS, Enabled: true, ListenPort: 10001, Settings: map[string]any{"transport": "tcp", "tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"}},
		{ID: "vmess", Name: "VMess", Type: serviceprotocol.TypeVMess, Enabled: true, ListenPort: 10002, Settings: map[string]any{"transport": "ws", "wsPath": "/vm", "wsMaxEarlyData": 0, "wsEarlyDataHeaderName": "Sec-WebSocket-Protocol", "tlsMode": "none"}},
		{ID: "hy2", Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, ListenPort: 10003, Settings: map[string]any{"serverName": "apple.com", "upMbps": 100, "downMbps": 100, "password": "pw"}},
		{ID: "tuic", Name: "TUIC", Type: serviceprotocol.TypeTUIC, Enabled: true, ListenPort: 10004, Settings: map[string]any{"serverName": "apple.com", "congestionControl": "bbr", "password": "pw"}},
		{ID: "anytls", Name: "AnyTLS", Type: serviceprotocol.TypeAnyTLS, Enabled: true, ListenPort: 10005, Settings: map[string]any{"serverName": "apple.com", "password": "pw"}},
		{ID: "ss", Name: "Shadowsocks", Type: serviceprotocol.TypeShadowsocks, Enabled: true, ListenPort: 10006, Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": "MTIzNDU2Nzg5MDEyMzQ1Ng=="}},
		{ID: "trojan", Name: "Trojan", Type: serviceprotocol.TypeTrojan, Enabled: true, ListenPort: 10007, Settings: map[string]any{"tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567", "password": "pw"}},
	}
	assignments := map[string]map[string]bool{"user": {}}
	for i := range protocols {
		if protocols[i].Listen == "" {
			protocols[i].Listen = "::"
		}
		assignments["user"][protocols[i].ID] = true
	}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", TLSCertPath: "/tls/cert.pem", TLSKeyPath: "/tls/key.pem", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var options option.Options
	if err := options.UnmarshalJSONContext(include.Context(context.Background()), result.ServerConfig); err != nil {
		t.Fatalf("sing-box rejects compiled config: %v", err)
	}
}

// TestClashPolicyRuleCompilesPortAndNetworkConditions：单条件规则的端口/
// 网络条件编译为 DST-PORT/NETWORK（此前被静默丢弃，dry-run 与产物分叉）；
// 历史遗留的组合行维持跳过——OR 展开只会比 AND 语义更宽。
func TestClashPolicyRuleCompilesPortAndNetworkConditions(t *testing.T) {
	portRule := servicepolicy.Rule{Action: "direct", Conditions: servicepolicy.Conditions{Ports: []int{443, 8443}}}
	lines, err := clashPolicyRule(portRule)
	if err != nil {
		t.Fatalf("compile port rule: %v", err)
	}
	if len(lines) != 2 || lines[0] != "DST-PORT,443,DIRECT" || lines[1] != "DST-PORT,8443,DIRECT" {
		t.Fatalf("port rule lines = %#v", lines)
	}

	networkRule := servicepolicy.Rule{Action: "block", Conditions: servicepolicy.Conditions{Networks: []string{"udp"}}}
	lines, err = clashPolicyRule(networkRule)
	if err != nil {
		t.Fatalf("compile network rule: %v", err)
	}
	if len(lines) != 1 || lines[0] != "NETWORK,udp,REJECT" {
		t.Fatalf("network rule lines = %#v", lines)
	}

	mixed := servicepolicy.Rule{Action: "direct", Conditions: servicepolicy.Conditions{Domains: []string{"corp.example.com"}, Ports: []int{443}}}
	lines, err = clashPolicyRule(mixed)
	if err != nil {
		t.Fatalf("compile mixed rule: %v", err)
	}
	if len(lines) != 1 || lines[0] != "DOMAIN,corp.example.com,DIRECT" {
		t.Fatalf("mixed rule lines = %#v, want domain line only", lines)
	}

	// 历史遗留的「端口+网络」组合行同样跳过：两类条件各自成行是 OR 语义，
	// 比评估器的 AND 更宽（写入期已拒绝该组合）。
	legacyPortsNetworks := servicepolicy.Rule{Action: "block", Conditions: servicepolicy.Conditions{Ports: []int{443}, Networks: []string{"udp"}}}
	lines, err = clashPolicyRule(legacyPortsNetworks)
	if err != nil {
		t.Fatalf("compile legacy ports+networks rule: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("legacy ports+networks lines = %#v, want none", lines)
	}
}
