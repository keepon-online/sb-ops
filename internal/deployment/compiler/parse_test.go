package compiler

import (
	"context"
	"testing"

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
