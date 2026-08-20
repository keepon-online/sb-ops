package singlevps

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

// seedProtocol inserts a protocol through the store so settings land encrypted
// exactly as the production write path stores them.
func seedProtocol(t *testing.T, db *sql.DB, id, name, protocolType string, enabled bool, listenPort int, settings map[string]any) {
	t.Helper()
	if settings == nil {
		settings = map[string]any{}
	}
	store := serviceprotocol.NewStore(db, secret.NewStore(db))
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	err := store.Create(context.Background(), serviceprotocol.Protocol{
		ID: id, Name: name, Type: serviceprotocol.Type(protocolType), Enabled: enabled,
		Listen: "::", ListenPort: listenPort, Settings: settings, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed protocol %s: %v", id, err)
	}
}

func itemByID(t *testing.T, result ProtocolRuntimeStatusResult, id string) ProtocolRuntimeItem {
	t.Helper()
	for _, item := range result.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("protocol %s missing from %#v", id, result.Items)
	return ProtocolRuntimeItem{}
}

func TestProtocolRuntimeStatusDerivesItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"connections":[
			{"id":"c1","start":"2026-08-01T12:00:00Z","metadata":{"host":"example.com","dstPort":"443","network":"tcp","type":"vless/vless-alpha"},"chains":["direct"],"upload":1,"download":2},
			{"id":"c2","start":"2026-08-01T12:00:01Z","metadata":{"host":"example.com","dstPort":"443","network":"tcp","type":"vless/vless-alpha"},"chains":["direct"],"upload":1,"download":2},
			{"id":"c3","start":"2026-08-01T12:00:02Z","metadata":{"host":"","dstPort":"443","network":"udp","type":"hysteria2/hysteria2-beta-hop"},"chains":["direct"],"upload":1,"download":2},
			{"id":"c4","start":"2026-08-01T12:00:03Z","metadata":{"host":"","dstPort":"80","network":"tcp","type":"mixed"},"chains":["direct"],"upload":1,"download":2}
		]}`))
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}

	db := testClashDB(t, net.JoinHostPort("127.0.0.1", port))
	seedProtocol(t, db, "proto-a", "alpha", "vless", true, 8443, nil)
	seedProtocol(t, db, "proto-b", "beta", "trojan", true, 443, nil)
	seedProtocol(t, db, "proto-c", "beta hop", "hysteria2", true, 3000, map[string]any{"portHopFrom": 30000, "portHopTo": 40000})
	seedProtocol(t, db, "proto-d", "delta", "vmess", false, 8444, nil)

	service := NewSyncService(db, ApplyConfig{})
	service.KernelState = func() runtimebox.Status { return runtimebox.Status{State: "running"} }
	service.ListenSnapshot = func(context.Context) (ListenSnapshot, error) {
		return ListenSnapshot{
			TCP: map[int]struct{}{8443: {}, 22: {}},
			UDP: map[int]struct{}{3000: {}, 53: {}},
		}, nil
	}
	service.PortHopSnapshot = func(context.Context) (string, error) {
		return "-A PREROUTING -p udp -m udp --dport 30000:40000 -j REDIRECT --to-ports 3000 -m comment --comment sb-port-hop-proto-c", nil
	}

	result, err := service.ProtocolRuntimeStatus(context.Background())
	if err != nil {
		t.Fatalf("ProtocolRuntimeStatus: %v", err)
	}
	if result.KernelStatus != "running" || !result.ListeningAvailable || !result.ConnectionsAvailable {
		t.Fatalf("result = %#v", result)
	}

	alpha := itemByID(t, result, "proto-a")
	if alpha.Status != "listening" || alpha.Transport != "tcp" || alpha.ActiveConnections == nil || *alpha.ActiveConnections != 2 {
		t.Fatalf("alpha = %#v", alpha)
	}
	beta := itemByID(t, result, "proto-b")
	if beta.Status != "not_listening" || beta.ActiveConnections == nil || *beta.ActiveConnections != 0 {
		t.Fatalf("beta = %#v", beta)
	}
	hop := itemByID(t, result, "proto-c")
	if hop.Status != "listening" || hop.Transport != "udp" || hop.PortHop == nil || hop.PortHop.Rule != "ok" || hop.PortHop.From != 30000 || hop.PortHop.To != 40000 {
		t.Fatalf("hop = %#v", hop)
	}
	if hop.ActiveConnections == nil || *hop.ActiveConnections != 1 {
		t.Fatalf("hop connections = %#v", hop.ActiveConnections)
	}
	delta := itemByID(t, result, "proto-d")
	if delta.Status != "disabled" || delta.ActiveConnections == nil || *delta.ActiveConnections != 0 || delta.PortHop != nil {
		t.Fatalf("delta = %#v", delta)
	}
}

func TestProtocolRuntimeStatusKernelDown(t *testing.T) {
	db := testSyncDatabase(t)
	seedProtocol(t, db, "proto-a", "alpha", "vless", true, 8443, nil)

	service := NewSyncService(db, ApplyConfig{})
	service.KernelState = func() runtimebox.Status { return runtimebox.Status{State: "stopped"} }
	service.ListenSnapshot = func(context.Context) (ListenSnapshot, error) {
		return ListenSnapshot{TCP: map[int]struct{}{8443: {}}}, nil
	}

	result, err := service.ProtocolRuntimeStatus(context.Background())
	if err != nil {
		t.Fatalf("ProtocolRuntimeStatus: %v", err)
	}
	if itemByID(t, result, "proto-a").Status != "kernel_down" {
		t.Fatalf("result = %#v", result.Items)
	}
}

func TestProtocolRuntimeStatusDegradesWhenProbesFail(t *testing.T) {
	db := testSyncDatabase(t) // no clash api enabled → connections unavailable
	seedProtocol(t, db, "proto-c", "beta hop", "hysteria2", true, 3000, map[string]any{"portHopFrom": 30000, "portHopTo": 40000})

	service := NewSyncService(db, ApplyConfig{})
	service.KernelState = func() runtimebox.Status { return runtimebox.Status{State: "running"} }
	service.ListenSnapshot = func(context.Context) (ListenSnapshot, error) {
		return ListenSnapshot{}, context.DeadlineExceeded
	}
	service.PortHopSnapshot = func(context.Context) (string, error) {
		return "", context.DeadlineExceeded
	}

	result, err := service.ProtocolRuntimeStatus(context.Background())
	if err != nil {
		t.Fatalf("ProtocolRuntimeStatus: %v", err)
	}
	if result.ListeningAvailable || result.ListeningReason == "" || result.ConnectionsAvailable || result.ConnectionsReason == "" {
		t.Fatalf("result = %#v", result)
	}
	hop := itemByID(t, result, "proto-c")
	if hop.Status != "unknown" || hop.ActiveConnections != nil || hop.PortHop == nil || hop.PortHop.Rule != "unknown" {
		t.Fatalf("hop = %#v", hop)
	}
}

func TestProtocolRuntimeStatusPortHopRuleStates(t *testing.T) {
	cases := []struct {
		name   string
		output string
		rule   string
	}{
		{"missing", "-A PREROUTING -j ACCEPT", "missing"},
		{"mismatched", "-A PREROUTING -p udp -m udp --dport 31000:32000 -j REDIRECT --to-ports 3000 -m comment --comment sb-port-hop-proto-c", "mismatched"},
		{"wrong target port", "-A PREROUTING -p udp -m udp --dport 30000:40000 -j REDIRECT --to-ports 3999 -m comment --comment sb-port-hop-proto-c", "mismatched"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db := testSyncDatabase(t)
			seedProtocol(t, db, "proto-c", "beta hop", "hysteria2", true, 3000, map[string]any{"portHopFrom": 30000, "portHopTo": 40000})
			service := NewSyncService(db, ApplyConfig{})
			service.KernelState = func() runtimebox.Status { return runtimebox.Status{State: "running"} }
			service.ListenSnapshot = func(context.Context) (ListenSnapshot, error) {
				return ListenSnapshot{UDP: map[int]struct{}{3000: {}}}, nil
			}
			service.PortHopSnapshot = func(context.Context) (string, error) { return testCase.output, nil }

			result, err := service.ProtocolRuntimeStatus(context.Background())
			if err != nil {
				t.Fatalf("ProtocolRuntimeStatus: %v", err)
			}
			hop := itemByID(t, result, "proto-c")
			if hop.PortHop == nil || hop.PortHop.Rule != testCase.rule {
				t.Fatalf("hop = %#v, want rule %s", hop, testCase.rule)
			}
		})
	}
}

func TestParsePortHopRules(t *testing.T) {
	output := `-A PREROUTING -j ACCEPT
-A PREROUTING -p udp -m udp --dport 30000:40000 -j REDIRECT --to-ports 3000 -m comment --comment sb-port-hop-proto-c
-A PREROUTING -p udp -m udp --dport 20000 -j REDIRECT --to-ports 2000 -m comment --comment "sb-port-hop-proto-x"
-A PREROUTING -j MARK --set-xmark 0x1/0x1`
	rules := parsePortHopRules(output)
	if len(rules) != 2 {
		t.Fatalf("rules = %#v", rules)
	}
	if rules[0].ID != "proto-c" || rules[0].From != 30000 || rules[0].To != 40000 || rules[0].ListenPort != 3000 {
		t.Fatalf("rules[0] = %#v", rules[0])
	}
	if rules[1].ID != "proto-x" || rules[1].From != 20000 || rules[1].To != 20000 || rules[1].ListenPort != 2000 {
		t.Fatalf("rules[1] = %#v", rules[1])
	}
}
