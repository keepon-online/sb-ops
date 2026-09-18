package compiler

import (
	"encoding/json"
	"testing"

	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

func TestCompileRendersManagedSingBoxConfiguration(t *testing.T) {
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{
			LogLevel: "debug", LogTimestamp: false,
			DNSServers: []servicesingbox.DNSServer{
				{Tag: "bootstrap", Address: "udp://223.5.5.5"},
				{Tag: "resolver", Address: "https://dns.example/dns-query", Detour: "proxy"},
			}, DNSFinal: "resolver",
			ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", V2RayAPIEnabled: false, V2RayAPIListen: "127.0.0.1:10000", DefaultOutboundTag: "proxy",
		},
		ClashAPISecret:    "clash-secret",
		Outbounds:         []servicesingbox.Outbound{{ID: "proxy", Tag: "proxy", Type: "socks", Server: "127.0.0.1", ServerPort: 1080, Username: "user"}},
		OutboundPasswords: map[string]string{"proxy": "upstream-secret"},
		RouteRules: []servicesingbox.RouteRule{
			{ID: "first", Name: "domain", Enabled: true, Priority: 10, Conditions: servicesingbox.RouteConditions{DomainSuffixes: []string{"example.com"}}, OutboundTag: "proxy"},
			{ID: "disabled", Name: "disabled", Enabled: false, Priority: 20, OutboundTag: "block"},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server map[string]any
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("unmarshal server config: %v", err)
	}
	log := server["log"].(map[string]any)
	if log["level"] != "debug" || log["timestamp"] != false {
		t.Fatalf("log = %#v", log)
	}
	outbounds := server["outbounds"].([]any)
	if len(outbounds) != 3 {
		t.Fatalf("outbounds = %#v", outbounds)
	}
	proxy := outbounds[2].(map[string]any)
	if proxy["password"] != "upstream-secret" || proxy["username"] != "user" {
		t.Fatalf("proxy outbound = %#v", proxy)
	}
	route := server["route"].(map[string]any)
	if route["final"] != "proxy" || len(route["rules"].([]any)) != 1 {
		t.Fatalf("route = %#v", route)
	}
	if resolver := route["default_domain_resolver"].(map[string]any)["server"]; resolver != "bootstrap" {
		t.Fatalf("default_domain_resolver = %#v", route["default_domain_resolver"])
	}
	dns := server["dns"].(map[string]any)
	if dns["final"] != "resolver" {
		t.Fatalf("dns = %#v", dns)
	}
	servers := dns["servers"].([]any)
	bootstrap := servers[0].(map[string]any)
	if bootstrap["type"] != "udp" || bootstrap["server"] != "223.5.5.5" {
		t.Fatalf("bootstrap server = %#v", bootstrap)
	}
	if _, ok := bootstrap["server_port"]; ok {
		t.Fatalf("未显式给出的端口应省略交给内核默认: %#v", bootstrap)
	}
	remote := servers[1].(map[string]any)
	if remote["type"] != "https" || remote["server"] != "dns.example" || remote["path"] != "/dns-query" || remote["detour"] != "proxy" {
		t.Fatalf("remote server = %#v", remote)
	}
	experimental := server["experimental"].(map[string]any)
	if experimental["clash_api"].(map[string]any)["external_controller"] != "127.0.0.1:9090" || experimental["clash_api"].(map[string]any)["secret"] != "clash-secret" {
		t.Fatalf("experimental = %#v", experimental)
	}
	if _, ok := experimental["v2ray_api"]; ok {
		t.Fatalf("disabled V2Ray API was rendered: %#v", experimental)
	}
}

func TestInputRejectsInvalidManagedRouteCondition(t *testing.T) {
	err := (Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "0.0.0.0:9090", V2RayAPIEnabled: true, V2RayAPIListen: "0.0.0.0:10000", DefaultOutboundTag: "direct"},
		RouteRules:    []servicesingbox.RouteRule{{Name: "bad", Enabled: true, OutboundTag: "direct", Conditions: servicesingbox.RouteConditions{IPCIDRs: []string{"bad-cidr"}}}},
	}).Validate()
	if err == nil {
		t.Fatal("invalid route CIDR unexpectedly accepted")
	}
}

func TestCompileRendersRuleSetConditions(t *testing.T) {
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", V2RayAPIEnabled: false, DefaultOutboundTag: "block"},
		RouteRules: []servicesingbox.RouteRule{
			{Name: "ruleset", Enabled: true, Priority: 10, OutboundTag: "direct", Conditions: servicesingbox.RouteConditions{RuleSets: []string{"ads"}}},
		},
		RuleSets:      []servicesingbox.RuleSet{{Tag: "ads", Format: "binary", URL: "https://example.com/ads.srs"}},
		CacheFilePath: "/data/singbox-cache.db",
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server map[string]any
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("unmarshal server config: %v", err)
	}
	rule := server["route"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["rule_set"].([]any)[0] != "ads" {
		t.Fatalf("rule conditions = %#v", rule)
	}
	definition := server["route"].(map[string]any)["rule_set"].([]any)[0].(map[string]any)
	if definition["type"] != "remote" || definition["tag"] != "ads" || definition["url"] != "https://example.com/ads.srs" {
		t.Fatalf("rule set definition = %#v", definition)
	}
	cacheFile := server["experimental"].(map[string]any)["cache_file"].(map[string]any)
	if cacheFile["enabled"] != true || cacheFile["path"] != "/data/singbox-cache.db" {
		t.Fatalf("cache_file = %#v", cacheFile)
	}
}

func TestInputRejectsRemovedGeoConditions(t *testing.T) {
	err := (Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", DefaultOutboundTag: "direct"},
		RouteRules:    []servicesingbox.RouteRule{{Name: "geo", Enabled: true, OutboundTag: "direct", Conditions: servicesingbox.RouteConditions{GeoIP: []string{"cn"}}}},
	}).Validate()
	if err == nil {
		t.Fatal("removed geoip condition unexpectedly accepted")
	}
}

func TestDNSServerAddressConversion(t *testing.T) {
	cases := []struct {
		name       string
		address    string
		want       map[string]any
		domainHost bool
	}{
		{"bare-ip-is-udp", "8.8.8.8", map[string]any{"type": "udp", "server": "8.8.8.8"}, false},
		{"udp-explicit-port", "udp://1.1.1.1:5353", map[string]any{"type": "udp", "server": "1.1.1.1", "server_port": uint16(5353)}, false},
		{"tcp", "tcp://9.9.9.9", map[string]any{"type": "tcp", "server": "9.9.9.9"}, false},
		{"tls", "tls://1.0.0.1", map[string]any{"type": "tls", "server": "1.0.0.1"}, false},
		{"ipv6-bracketed", "udp://[2606:4700:4700::1111]:53", map[string]any{"type": "udp", "server": "2606:4700:4700::1111", "server_port": uint16(53)}, false},
		{"https-with-path", "https://dns.google/dns-query", map[string]any{"type": "https", "server": "dns.google", "path": "/dns-query"}, true},
		{"https-port-and-path", "https://1.1.1.1:8443/doH", map[string]any{"type": "https", "server": "1.1.1.1", "server_port": uint16(8443), "path": "/doH"}, false},
		{"h3", "h3://dns.google", map[string]any{"type": "h3", "server": "dns.google"}, true},
		{"quic", "quic://dns.adguard.com", map[string]any{"type": "quic", "server": "dns.adguard.com"}, true},
		{"local", "local", map[string]any{"type": "local"}, false},
		{"dhcp-auto", "dhcp://auto", map[string]any{"type": "dhcp"}, false},
		{"dhcp-interface", "dhcp://eth0", map[string]any{"type": "dhcp", "interface": "eth0"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, domainHost, err := dnsServerFromAddress("t", tc.address, "")
			if err != nil {
				t.Fatalf("convert %q: %v", tc.address, err)
			}
			if domainHost != tc.domainHost {
				t.Fatalf("convert %q domainHost=%v, want %v", tc.address, domainHost, tc.domainHost)
			}
			value["tag"] = "t"
			for key, want := range tc.want {
				if value[key] != want {
					t.Fatalf("convert %q [%s]=%#v, want %#v", tc.address, key, value[key], want)
				}
			}
			if len(value) != len(tc.want)+1 {
				t.Fatalf("convert %q extra fields: %#v", tc.address, value)
			}
		})
	}
	if _, _, err := dnsServerFromAddress("t", "rcode://success", ""); err == nil {
		t.Fatal("unsupported scheme unexpectedly accepted")
	}
}

func TestCompileRejectsDomainOnlyDNSServers(t *testing.T) {
	_, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{
			LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", DefaultOutboundTag: "direct",
			DNSServers: []servicesingbox.DNSServer{{Tag: "only", Address: "https://dns.example/dns-query"}}, DNSFinal: "only",
		},
	})
	if err == nil {
		t.Fatal("domain-only DNS servers unexpectedly accepted")
	}
}

func TestInputRejectsUnknownRuleSetReference(t *testing.T) {
	err := (Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", DefaultOutboundTag: "direct"},
		RouteRules:    []servicesingbox.RouteRule{{Name: "bad", Enabled: true, OutboundTag: "direct", Conditions: servicesingbox.RouteConditions{RuleSets: []string{"missing"}}}},
	}).Validate()
	if err == nil {
		t.Fatal("unknown rule set reference unexpectedly accepted")
	}
}

func TestCompileRendersWarpEndpoint(t *testing.T) {
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", DefaultOutboundTag: "warp"},
		Outbounds: []servicesingbox.Outbound{
			{ID: "proxy", Tag: "proxy", Type: "socks", Server: "127.0.0.1", ServerPort: 1080},
			{ID: "warp", Tag: "warp", Type: "warp", Warp: &servicesingbox.WarpSettings{
				Endpoint: "engage.cloudflareclient.com:2408", PublicKey: "pub-key", PrivateKey: "priv-key",
				IPv4: "172.16.0.2/32", IPv6: "2606:4700:110:8a12::/128", Reserved: []int{1, 2, 3}, MTU: 1408,
			}},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server map[string]any
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("unmarshal server config: %v", err)
	}
	endpoints, ok := server["endpoints"].([]any)
	if !ok || len(endpoints) != 1 {
		t.Fatalf("endpoints = %#v", server["endpoints"])
	}
	endpoint := endpoints[0].(map[string]any)
	if endpoint["type"] != "wireguard" || endpoint["tag"] != "warp" || endpoint["private_key"] != "priv-key" || endpoint["mtu"] != float64(1408) {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	address := endpoint["address"].([]any)
	if len(address) != 2 || address[0] != "172.16.0.2/32" || address[1] != "2606:4700:110:8a12::/128" {
		t.Fatalf("endpoint address = %#v", address)
	}
	peer := endpoint["peers"].([]any)[0].(map[string]any)
	if peer["address"] != "engage.cloudflareclient.com" || peer["port"] != float64(2408) || peer["public_key"] != "pub-key" {
		t.Fatalf("peer = %#v", peer)
	}
	allowedIPs := peer["allowed_ips"].([]any)
	if len(allowedIPs) != 2 || allowedIPs[0] != "0.0.0.0/0" || allowedIPs[1] != "::/0" {
		t.Fatalf("allowed_ips = %#v", allowedIPs)
	}
	reserved := peer["reserved"].([]any)
	if len(reserved) != 3 || reserved[0] != float64(1) {
		t.Fatalf("reserved = %#v", reserved)
	}
	outbounds := server["outbounds"].([]any)
	if len(outbounds) != 3 {
		t.Fatalf("outbounds = %#v", outbounds)
	}
	route := server["route"].(map[string]any)
	if route["final"] != "warp" {
		t.Fatalf("route final = %#v", route["final"])
	}
}

func TestCompileRejectsMalformedWarpEndpoint(t *testing.T) {
	_, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		SingBoxConfig: servicesingbox.Config{LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090", DefaultOutboundTag: "direct"},
		Outbounds: []servicesingbox.Outbound{
			{ID: "warp", Tag: "warp", Type: "warp", Warp: &servicesingbox.WarpSettings{Endpoint: "no-port", PublicKey: "pub", PrivateKey: "priv", IPv4: "172.16.0.2/32"}},
		},
	})
	if err == nil {
		t.Fatal("malformed WARP endpoint unexpectedly accepted")
	}
}
