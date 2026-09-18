package probe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
)

// TestDumpClientConfigsForExternalCheck 供外部 `sing-box check` 核验拨测
// 客户端配置字段：SB_PROBE_DUMP=<dir> go test -run TestDumpClientConfigsForExternalCheck ./internal/probe
func TestDumpClientConfigsForExternalCheck(t *testing.T) {
	dir := os.Getenv("SB_PROBE_DUMP")
	if dir == "" {
		t.Skip("set SB_PROBE_DUMP=<dir> to dump probe client configs")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	realityKey := "d89wx6z5zHVbQHBaI3eYdyystFqRaq2c7tOM_Uf6Chk"
	cases := map[string]map[string]any{
		"vless-reality": {"transport": "tcp", "tlsMode": "reality", "serverName": "www.apple.com", "realityPublicKey": realityKey, "shortId": "abcd"},
		"vless-ws-cert": {"transport": "ws", "tlsMode": "certificate", "serverName": "203.0.113.10", "wsPath": "/ws"},
		"vmess-ws":      {"transport": "ws", "wsPath": "/vm"},
		"trojan-cert":   {"tlsMode": "certificate", "serverName": "203.0.113.10"},
		"hy2":           {"serverName": "www.bing.com", "upMbps": 100, "downMbps": 100},
		"tuic":          {"serverName": "www.bing.com", "congestionControl": "bbr"},
		"anytls":        {"serverName": "www.bing.com"},
	}
	types := map[string]serviceprotocol.Type{
		"vless-reality": serviceprotocol.TypeVLESS, "vless-ws-cert": serviceprotocol.TypeVLESS,
		"vmess-ws": serviceprotocol.TypeVMess, "trojan-cert": serviceprotocol.TypeTrojan,
		"hy2": serviceprotocol.TypeHysteria2, "tuic": serviceprotocol.TypeTUIC, "anytls": serviceprotocol.TypeAnyTLS,
	}
	for name, settings := range cases {
		outbound, err := clientOutbound(serviceprotocol.Protocol{
			ID: "p1", Name: name, Type: types[name], Enabled: true, Listen: "::", ListenPort: 18443, Settings: settings,
		}, probeDevice)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		config := map[string]any{
			"log":       map[string]any{"disabled": true},
			"inbounds":  []any{map[string]any{"type": "mixed", "tag": "probe-in", "listen": "127.0.0.1", "listen_port": 18080}},
			"outbounds": []any{outbound, map[string]any{"type": "direct", "tag": "direct"}},
			"route": map[string]any{
				"rules": []any{map[string]any{"inbound": []string{"probe-in"}, "outbound": "probe-out"}},
				"final": "direct",
			},
		}
		content, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), content, 0o600); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
