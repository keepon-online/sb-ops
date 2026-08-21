package compiler

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	"gopkg.in/yaml.v3"
)

const (
	testDeviceID   = "11111111-1111-1111-1111-111111111111"
	testDeviceUUID = "00000000-0000-0000-0000-000000000001"
)

// testDevice builds the standard single-device fixture carrying the user's
// credential material.
func testDevice(userID, password string) serviceuser.ArtifactDevice {
	return serviceuser.ArtifactDevice{UserID: userID, DeviceID: testDeviceID, Enabled: true, UUID: testDeviceUUID, Password: password}
}

// subscriptionFor returns the (single-device) subscription content for a user.
func subscriptionFor(result Result, userID string) []byte {
	for _, subscription := range result.Subscriptions {
		if subscription.UserID == userID {
			return subscription.Content
		}
	}
	return nil
}

func TestInputAcceptsPublicHostname(t *testing.T) {
	input := Input{PublicAddress: "panel.example.com"}
	if err := input.Validate(); err != nil {
		t.Fatalf("Validate hostname: %v", err)
	}
	for _, address := range []string{"http://panel.example.com", "panel.example.com:443", "localhost", "127.0.0.1", "10.0.0.1"} {
		input.PublicAddress = address
		if err := input.Validate(); err == nil {
			t.Fatalf("Validate(%q) unexpectedly succeeded", address)
		}
	}
}

func TestInputAllowsPrivateAddressInDevMode(t *testing.T) {
	for _, address := range []string{"192.168.2.166", "127.0.0.1", "10.0.0.1"} {
		input := Input{PublicAddress: address, AllowPrivateAddress: true}
		if err := input.Validate(); err != nil {
			t.Fatalf("dev-mode Validate(%q): %v", address, err)
		}
	}
	// Still rejects malformed input even in dev mode.
	if err := (Input{PublicAddress: "http://192.168.2.166", AllowPrivateAddress: true}).Validate(); err == nil {
		t.Fatal("dev mode accepted malformed public address")
	}
}

func TestCompileEnablesPerUserTrafficStats(t *testing.T) {
	previousNow := inputNow
	inputNow = func() time.Time { return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { inputNow = previousNow })

	user := serviceuser.ArtifactUser{ID: "user-1", Name: "Subscription User", Enabled: true}
	device := testDevice("user-1", "credential-password")
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		Protocols: []serviceprotocol.Protocol{
			{ID: "vless", Name: "VLESS", Type: serviceprotocol.TypeVLESS, Enabled: true, Listen: "::", ListenPort: 2443, Settings: map[string]any{"tlsMode": "reality", "serverName": "example.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"}},
			{ID: "ss", Name: "SS-2022", Type: serviceprotocol.TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444, Settings: map[string]any{"method": "2022-blake3-aes-256-gcm", "password": "server-password"}},
		},
		Users:         []serviceuser.ArtifactUser{user},
		Devices:       []serviceuser.ArtifactDevice{device},
		UserProtocols: map[string]map[string]bool{"user-1": {"vless": true, "ss": true}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	var server struct {
		Experimental map[string]any   `json:"experimental"`
		Inbounds     []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	stats := server.Experimental["v2ray_api"].(map[string]any)["stats"].(map[string]any)
	if users := stats["users"].([]any); len(users) != 1 || users[0] != testDeviceID {
		t.Fatalf("V2Ray stats users = %#v, want %q", users, testDeviceID)
	}
	byType := map[string]map[string]any{}
	for _, inbound := range server.Inbounds {
		byType[inbound["type"].(string)] = inbound
	}
	if name := byType["vless"]["users"].([]any)[0].(map[string]any)["name"]; name != testDeviceID {
		t.Fatalf("VLESS user name = %#v, want device ID %q", name, testDeviceID)
	}
	ssUsers := byType["shadowsocks"]["users"].([]any)
	if len(ssUsers) != 1 || ssUsers[0].(map[string]any)["name"] != testDeviceID {
		t.Fatalf("Shadowsocks users = %#v, want device ID %q", ssUsers, testDeviceID)
	}
	digest := sha256.Sum256([]byte(device.Password))
	wantUserKey := base64.StdEncoding.EncodeToString(digest[:])
	if password := ssUsers[0].(map[string]any)["password"]; password != wantUserKey {
		t.Fatalf("Shadowsocks user password = %#v, want derived subscription credential", password)
	}
	var subscription struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(subscriptionFor(result, user.ID), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	serverKey := "server-password"
	for _, proxy := range subscription.Proxies {
		if proxy["type"] == "ss" && proxy["password"] != serverKey+":"+wantUserKey {
			t.Fatalf("Shadowsocks subscription password = %#v, want composite server:user key", proxy["password"])
		}
	}
}

func TestCompileClashSubscriptionIncludesCommonRules(t *testing.T) {
	previousNow := inputNow
	inputNow = func() time.Time { return time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { inputNow = previousNow })

	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		Protocols: []serviceprotocol.Protocol{{
			ID: "ss", Name: "SS", Type: serviceprotocol.TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444,
			Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": "test-password"},
		}},
		Users:         []serviceuser.ArtifactUser{{ID: "user", Name: "test", Enabled: true}},
		Devices:       []serviceuser.ArtifactDevice{testDevice("user", "credential-password")},
		UserProtocols: map[string]map[string]bool{"user": {"ss": true}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	var subscription map[string]any
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	if subscription["port"] != 7890 || subscription["socks-port"] != 7891 {
		t.Fatalf("ports = %#v, want HTTP 7890 and SOCKS 7891", subscription)
	}
	if _, ok := subscription["mixed-port"]; ok {
		t.Fatalf("subscription must use separate HTTP and SOCKS ports: %#v", subscription)
	}
	dns := subscription["dns"].(map[string]any)
	if dns["enhanced-mode"] != "fake-ip" || dns["respect-rules"] != true || !reflect.DeepEqual(dns["nameserver"], []any{"https://120.53.53.53/dns-query", "https://223.5.5.5/dns-query"}) {
		t.Fatalf("dns = %#v", dns)
	}
	providers := subscription["rule-providers"].(map[string]any)
	if len(providers) != 39 {
		t.Fatalf("rule providers = %#v, want 39 providers", providers)
	}
	for name, want := range map[string]map[string]any{
		"category-ads-all": {"behavior": "domain", "format": "mrs", "path": "./ruleset/category-ads-all.mrs", "url": "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geosite/category-ads-all.mrs"},
		"google-ip":        {"behavior": "ipcidr", "format": "mrs", "path": "./ruleset/google-ip.mrs", "url": "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/geoip/google-ip.mrs"},
		"claude":           {"behavior": "classical", "format": "yaml", "path": "./ruleset/claude.yaml", "url": "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash/Claude/Claude.yaml"},
	} {
		provider, ok := providers[name].(map[string]any)
		if !ok || provider["type"] != "http" || provider["interval"] != 86400 || provider["proxy"] != clashProxyGroup {
			t.Fatalf("rule provider %q = %#v", name, provider)
		}
		for field, expected := range want {
			if provider[field] != expected {
				t.Fatalf("rule provider %q field %s = %#v, want %#v", name, field, provider[field], expected)
			}
		}
	}
	groups := subscription["proxy-groups"].([]any)
	if len(groups) != 18 {
		t.Fatalf("proxy groups = %#v, want 18 groups", groups)
	}
	byName := map[string]map[string]any{}
	for _, value := range groups {
		group := value.(map[string]any)
		byName[group["name"].(string)] = group
	}
	if group := byName[clashProxyGroup]; group["type"] != "select" || !reflect.DeepEqual(group["proxies"], []any{"DIRECT", "REJECT", clashAutoGroup, "SS"}) {
		t.Fatalf("node selector = %#v", group)
	}
	if group := byName[clashAutoGroup]; group["type"] != "url-test" || !reflect.DeepEqual(group["proxies"], []any{"SS"}) || group["url"] != "https://www.gstatic.com/generate_204" || group["interval"] != 300 || group["tolerance"] != 50 {
		t.Fatalf("automatic selector = %#v", group)
	}
	for _, name := range []string{clashAdsGroup, clashAIGroup, clashBilibiliGroup, clashYouTubeGroup, clashGoogleGroup, clashPrivateGroup, clashChinaGroup, clashTelegramGroup, clashGitHubGroup, clashMicrosoftGroup, clashAppleGroup, clashSocialGroup, clashStreamingGroup, clashEducationGroup, clashServiceGroup, clashFinalGroup} {
		group := byName[name]
		if group["type"] != "select" {
			t.Fatalf("category selector %q = %#v", name, group)
		}
	}

	rules := subscription["rules"].([]any)
	actualRules := make([]string, len(rules))
	for i, rule := range rules {
		actualRules[i] = rule.(string)
	}
	if !reflect.DeepEqual(actualRules, defaultClashRules()) || !containsRule(actualRules, "RULE-SET,category-ads-all,"+clashAdsGroup) || !containsRule(actualRules, "RULE-SET,cn,"+clashChinaGroup+",no-resolve") || actualRules[len(actualRules)-1] != "MATCH,"+clashFinalGroup {
		t.Fatalf("rules = %#v", actualRules)
	}
}

func containsRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}

func TestClashRulesUseAssignedPolicy(t *testing.T) {
	rules, err := clashRules(Input{
		Policies: []servicepolicy.Policy{{ID: "default", Name: "Clash 默认分流", Enabled: true, DefaultAction: "proxy"}},
		PolicyRules: map[string][]servicepolicy.Rule{"default": {
			{Name: "AI", Enabled: true, Action: "proxy", Conditions: servicepolicy.Conditions{RuleSets: []string{"openai", "claude"}}},
			{Name: "China", Enabled: true, Action: "direct", Conditions: servicepolicy.Conditions{RuleSets: []string{"china"}}},
			{Name: "Private provider", Enabled: true, Action: "proxy", NoResolve: true, Conditions: servicepolicy.Conditions{RuleSets: []string{"private"}}},
			{Name: "Private", Enabled: true, Action: "direct", NoResolve: true, Conditions: servicepolicy.Conditions{IPCIDRs: []string{"10.0.0.0/8"}}},
		}},
		UserPolicies: map[string]map[string]bool{"user": {"default": true}},
	}, serviceuser.ArtifactUser{ID: "user"})
	if err != nil {
		t.Fatalf("compile policy rules: %v", err)
	}
	want := []string{"RULE-SET,openai," + clashAIGroup, "RULE-SET,claude," + clashAIGroup, "RULE-SET,china,DIRECT", "RULE-SET,private," + clashPrivateGroup + ",no-resolve", "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", "MATCH," + clashProxyGroup}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %#v, want %#v", rules, want)
	}
}

func TestCompileShadowsocksSubscription(t *testing.T) {
	previousNow := inputNow
	inputNow = func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { inputNow = previousNow })

	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		Protocols: []serviceprotocol.Protocol{{
			ID: "ss", Name: "SS-2022", Type: serviceprotocol.TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444,
			Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": "test-password"},
		}},
		Users:         []serviceuser.ArtifactUser{{ID: "user", Name: "test", Enabled: true}},
		Devices:       []serviceuser.ArtifactDevice{testDevice("user", "credential-password")},
		UserProtocols: map[string]map[string]bool{"user": {"ss": true}},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	var server map[string]any
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	if server["experimental"].(map[string]any)["clash_api"].(map[string]any)["external_controller"] != "127.0.0.1:9090" {
		t.Fatalf("Clash API is not enabled for traffic collection: %#v", server["experimental"])
	}
	inbound := server["inbounds"].([]any)[0].(map[string]any)
	if inbound["type"] != "shadowsocks" || inbound["listen_port"] != float64(2444) || inbound["method"] != "2022-blake3-aes-128-gcm" {
		t.Fatalf("unexpected Shadowsocks inbound: %#v", inbound)
	}

	var subscription map[string]any
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	proxy := subscription["proxies"].([]any)[0].(map[string]any)
	if proxy["type"] != "ss" || proxy["cipher"] != "2022-blake3-aes-128-gcm" || proxy["port"] != 2444 {
		t.Fatalf("unexpected Shadowsocks proxy: %#v", proxy)
	}
	digest := sha256.Sum256([]byte("credential-password"))
	// SS-2022 多用户：客户端密码 = 服务端密钥:派生用户密钥。
	wantPassword := "test-password:" + base64.StdEncoding.EncodeToString(digest[:16])
	if !strings.Contains(string(subscriptionFor(result, "user")), "password: "+wantPassword) {
		t.Fatal("subscription is missing the composite Shadowsocks 2022 password")
	}
}

func TestCompileLegacyProtocolSet(t *testing.T) {
	previousNow := inputNow
	inputNow = func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { inputNow = previousNow })

	protocols := []serviceprotocol.Protocol{
		{ID: "vless", Name: "VLESS", Type: serviceprotocol.TypeVLESS, Enabled: true, Listen: "::", ListenPort: 2443, Settings: map[string]any{"tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"}},
		{ID: "vmess", Name: "VMess WS", Type: serviceprotocol.TypeVMess, Enabled: true, Listen: "::", ListenPort: 2445, Settings: map[string]any{"transport": "ws", "wsPath": "/legacy-vm", "wsMaxEarlyData": 2048, "wsEarlyDataHeaderName": "Sec-WebSocket-Protocol"}},
		{ID: "hy2", Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, Listen: "::", ListenPort: 2446, Settings: map[string]any{"serverName": "www.bing.com", "upMbps": 100, "downMbps": 100}},
		{ID: "tuic", Name: "TUIC", Type: serviceprotocol.TypeTUIC, Enabled: true, Listen: "::", ListenPort: 2447, Settings: map[string]any{"serverName": "www.bing.com", "congestionControl": "bbr"}},
		{ID: "anytls", Name: "AnyTLS", Type: serviceprotocol.TypeAnyTLS, Enabled: true, Listen: "::", ListenPort: 2448, Settings: map[string]any{"serverName": "www.bing.com"}},
	}
	user := serviceuser.ArtifactUser{ID: "user", Name: "legacy", Enabled: true}
	device := testDevice("user", "legacy-password")
	assignments := map[string]map[string]bool{"user": {"vless": true, "vmess": true, "hy2": true, "tuic": true, "anytls": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", TLSCertPath: "/etc/sb/tls/fullchain.pem", TLSKeyPath: "/etc/sb/tls/privkey.pem", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	if len(server.Inbounds) != 5 {
		t.Fatalf("inbounds = %d, want 5", len(server.Inbounds))
	}
	byType := map[string]map[string]any{}
	for _, inbound := range server.Inbounds {
		byType[inbound["type"].(string)] = inbound
	}
	vmess := byType["vmess"]
	transport := vmess["transport"].(map[string]any)
	if transport["type"] != "ws" || transport["path"] != "/legacy-vm" || transport["max_early_data"] != float64(2048) {
		t.Fatalf("unexpected VMess transport: %#v", transport)
	}
	for _, kind := range []string{"hysteria2", "tuic"} {
		tls := byType[kind]["tls"].(map[string]any)
		if tls["alpn"].([]any)[0] != "h3" {
			t.Fatalf("%s tls ALPN = %#v, want h3", kind, tls["alpn"])
		}
	}
	if byType["tuic"]["users"].([]any)[0].(map[string]any)["password"] != "legacy-password" {
		t.Fatalf("TUIC does not use the user password: %#v", byType["tuic"])
	}
	if byType["anytls"]["users"].([]any)[0].(map[string]any)["password"] != "legacy-password" {
		t.Fatalf("AnyTLS does not use the user password: %#v", byType["anytls"])
	}

	var subscription struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	if len(subscription.Proxies) != 5 {
		t.Fatalf("subscription proxies = %d, want 5", len(subscription.Proxies))
	}
	proxies := map[string]map[string]any{}
	for _, proxy := range subscription.Proxies {
		proxies[proxy["type"].(string)] = proxy
	}
	if proxies["vmess"]["ws-opts"].(map[string]any)["path"] != "/legacy-vm" {
		t.Fatalf("VMess subscription path = %#v", proxies["vmess"])
	}
if proxies["tuic"]["congestion-controller"] != "bbr" || proxies["tuic"]["skip-cert-verify"] != false {
			t.Fatalf("unexpected TUIC subscription: %#v", proxies["tuic"])
		}
		if proxies["anytls"]["password"] != "legacy-password" || proxies["anytls"]["skip-cert-verify"] != false {
		t.Fatalf("unexpected AnyTLS subscription: %#v", proxies["anytls"])
	}
}

func TestCompileUsesProtocolCertificatePaths(t *testing.T) {
	certificateID := "certificate-1"
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		TLSCertPath:   "/etc/sb/tls/fallback-cert.pem",
		TLSKeyPath:    "/etc/sb/tls/fallback-key.pem",
		CertificatePaths: map[string]CertificatePaths{certificateID: {
			CertificatePath: "/etc/sing-box/certificates/certificate-1/2/cert.pem",
			KeyPath:         "/etc/sing-box/certificates/certificate-1/2/key.pem",
		}},
		Protocols: []serviceprotocol.Protocol{{
			ID: "hy2", Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, Listen: "::", ListenPort: 2446,
			CertificateID: &certificateID, Settings: map[string]any{"serverName": "panel.example.com", "upMbps": 100, "downMbps": 100},
		}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	tls := server.Inbounds[0]["tls"].(map[string]any)
	if tls["certificate_path"] != "/etc/sing-box/certificates/certificate-1/2/cert.pem" || tls["key_path"] != "/etc/sing-box/certificates/certificate-1/2/key.pem" {
		t.Fatalf("TLS paths = %#v", tls)
	}
}

func TestValidateRejectsTUICUDPPortCollision(t *testing.T) {
	err := (Input{PublicAddress: "1.1.1.1", TLSCertPath: "cert.pem", TLSKeyPath: "key.pem", Protocols: []serviceprotocol.Protocol{
		{ID: "hy2", Name: "Hysteria2", Type: serviceprotocol.TypeHysteria2, Enabled: true, ListenPort: 2446},
		{ID: "tuic", Name: "TUIC", Type: serviceprotocol.TypeTUIC, Enabled: true, ListenPort: 2446},
	}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "相同的 udp:") {
		t.Fatalf("Validate error = %v, want UDP port collision", err)
	}
}

func TestValidateRejectsTrojanRealityWithoutPrivateKey(t *testing.T) {
	err := (Input{PublicAddress: "1.1.1.1", Protocols: []serviceprotocol.Protocol{{
		ID: "trojan", Name: "Trojan", Type: serviceprotocol.TypeTrojan, Enabled: true, ListenPort: 3001,
		Settings: map[string]any{"tlsMode": "reality", "serverName": "apple.com"},
	}}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "realityPrivateKey") {
		t.Fatalf("Validate error = %v, want missing realityPrivateKey", err)
	}
}

func TestCompileRendersVLESSTransportAndVMessTLS(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "legacy", Enabled: true}
	device := testDevice("user", "p")
	protocols := []serviceprotocol.Protocol{
		{ID: "vless", Name: "VLESS grpc", Type: serviceprotocol.TypeVLESS, Enabled: true, ListenPort: 2443, Settings: map[string]any{"transport": "grpc", "grpcServiceName": "Tun", "tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567", "handshakeServerPort": 8443}},
		{ID: "vmess", Name: "VMess tls", Type: serviceprotocol.TypeVMess, Enabled: true, ListenPort: 2445, Settings: map[string]any{"transport": "ws", "wsPath": "/vm", "tlsMode": "certificate", "serverName": "panel.example.com"}},
	}
	assignments := map[string]map[string]bool{"user": {"vless": true, "vmess": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", TLSCertPath: "/tls/cert.pem", TLSKeyPath: "/tls/key.pem", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	byType := map[string]map[string]any{}
	for _, inbound := range server.Inbounds {
		byType[inbound["type"].(string)] = inbound
	}
	vless := byType["vless"]
	if transport := vless["transport"].(map[string]any); transport["type"] != "grpc" || transport["service_name"] != "Tun" {
		t.Fatalf("vless transport = %#v", vless["transport"])
	}
	reality := vless["tls"].(map[string]any)["reality"].(map[string]any)
	if port := reality["handshake"].(map[string]any)["server_port"]; port != float64(8443) {
		t.Fatalf("reality handshake port = %#v", port)
	}
	vmess := byType["vmess"]
	if tls, ok := vmess["tls"].(map[string]any); !ok || tls["enabled"] != true || tls["server_name"] != "panel.example.com" {
		t.Fatalf("vmess tls = %#v", vmess["tls"])
	}
}

func TestCompileRendersTrojan(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "u", Enabled: true}
	device := testDevice("user", "pw")
	protocols := []serviceprotocol.Protocol{
		{ID: "trojan", Name: "Trojan", Type: serviceprotocol.TypeTrojan, Enabled: true, ListenPort: 3001, Settings: map[string]any{"tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"}},
	}
	assignments := map[string]map[string]bool{"user": {"trojan": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", TLSCertPath: "/tls/cert.pem", TLSKeyPath: "/tls/key.pem", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	byType := map[string]map[string]any{}
	for _, inbound := range server.Inbounds {
		byType[inbound["type"].(string)] = inbound
	}
	if byType["trojan"] == nil {
		t.Fatalf("missing trojan inbound: %#v", byType)
	}
}

func TestCompileTrojanRenderedInSubscription(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "u", Enabled: true}
	device := testDevice("user", "pw")
	protocol := serviceprotocol.Protocol{
		ID: "trojan", Name: "Trojan", Type: serviceprotocol.TypeTrojan, Enabled: true, ListenPort: 3001,
		Settings: map[string]any{"tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"},
	}
	assignments := map[string]map[string]bool{"user": {"trojan": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", Protocols: []serviceprotocol.Protocol{protocol}, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var subscription struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	if len(subscription.Proxies) != 1 || subscription.Proxies[0]["type"] != "trojan" {
		t.Fatalf("subscription proxies = %#v, want one trojan", subscription.Proxies)
	}
	if opts := subscription.Proxies[0]["reality-opts"]; opts == nil {
		t.Fatalf("trojan reality-opts missing: %#v", subscription.Proxies[0])
	}
}

func TestVLESSTransportReflectedInSubscriptionAndFlow(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "u", Enabled: true}
	device := testDevice("user", "pw")
	protocols := []serviceprotocol.Protocol{
		{ID: "grpc", Name: "VLESS grpc", Type: serviceprotocol.TypeVLESS, Enabled: true, ListenPort: 2443, Settings: map[string]any{"transport": "grpc", "grpcServiceName": "Tun", "tlsMode": "reality", "serverName": "apple.com", "realityPrivateKey": "private", "realityPublicKey": "public", "shortId": "01234567"}},
	}
	assignments := map[string]map[string]bool{"user": {"grpc": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	users := server.Inbounds[0]["users"].([]any)[0].(map[string]any)
	if _, hasFlow := users["flow"]; hasFlow {
		t.Fatalf("grpc VLESS should not carry tcp-only flow: %#v", users)
	}
	var subscription struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	proxy := subscription.Proxies[0]
	if proxy["network"] != "grpc" || proxy["grpc-opts"].(map[string]any)["grpc-service-name"] != "Tun" {
		t.Fatalf("grpc subscription proxy = %#v", proxy)
	}
	if _, hasFlow := proxy["flow"]; hasFlow {
		t.Fatalf("grpc subscription should not carry flow: %#v", proxy)
	}
}

func TestCompileDerivesUniqueInboundTags(t *testing.T) {
	user := serviceuser.ArtifactUser{ID: "user", Name: "u", Enabled: true}
	device := testDevice("user", "pw")
	protocols := []serviceprotocol.Protocol{
		{ID: "h1", Name: "h2", Type: serviceprotocol.TypeHysteria2, Enabled: true, ListenPort: 2443, Settings: map[string]any{"serverName": "www.bing.com"}},
		{ID: "h2", Name: "h22", Type: serviceprotocol.TypeHysteria2, Enabled: true, ListenPort: 2444, Settings: map[string]any{"serverName": "www.bing.com"}},
	}
	assignments := map[string]map[string]bool{"user": {"h1": true, "h2": true}}
	result, err := Compile(Input{PublicAddress: "1.1.1.1", TLSCertPath: "/tls/cert.pem", TLSKeyPath: "/tls/key.pem", Protocols: protocols, Users: []serviceuser.ArtifactUser{user}, Devices: []serviceuser.ArtifactDevice{device}, UserProtocols: assignments})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var server struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(result.ServerConfig, &server); err != nil {
		t.Fatalf("parse server config: %v", err)
	}
	tags := map[string]bool{}
	for _, inbound := range server.Inbounds {
		tag := inbound["tag"].(string)
		if tags[tag] {
			t.Fatalf("duplicate inbound tag %q", tag)
		}
		tags[tag] = true
	}
	if !tags["hysteria2-h2"] || !tags["hysteria2-h22"] {
		t.Fatalf("unexpected tags: %#v", tags)
	}
}

func TestCompileRendersArgoSubscriptionNodes(t *testing.T) {
	result, err := Compile(Input{
		PublicAddress: "1.1.1.1",
		Protocols: []serviceprotocol.Protocol{{
			ID: "vm", Name: "VMess", Type: serviceprotocol.TypeVMess, Enabled: true, Listen: "::", ListenPort: 2096,
			Settings: map[string]any{"transport": "ws", "wsPath": "/argo"},
		}},
		Users:         []serviceuser.ArtifactUser{{ID: "user", Name: "test", Enabled: true}},
		Devices:       []serviceuser.ArtifactDevice{testDevice("user", "credential-password")},
		UserProtocols: map[string]map[string]bool{"user": {"vm": true}},
		ArgoTunnels: []serviceargo.Tunnel{
			{ID: "t3", Name: "优选", Enabled: true, Mode: serviceargo.ModeToken, ProtocolID: "vm", AssignedDomain: "tunnel.example.com", PreferredAddress: "preferred.example.net"},
			{ID: "t1", Name: "重复协议", Enabled: true, Mode: serviceargo.ModeTemporary, ProtocolID: "vm", AssignedDomain: "random-word.trycloudflare.com"},
			{ID: "t2", Name: "未就绪", Enabled: true, Mode: serviceargo.ModeTemporary, ProtocolID: "vm"},
		},
	})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var subscription map[string]any
	if err := yaml.Unmarshal(subscriptionFor(result, "user"), &subscription); err != nil {
		t.Fatalf("parse subscription: %v", err)
	}
	proxies := subscription["proxies"].([]any)
	if len(proxies) != 2 { // 原生 vmess + 唯一一条已就绪隧道
		t.Fatalf("proxies = %#v", proxies)
	}
	byName := map[string]map[string]any{}
	for _, item := range proxies {
		proxy := item.(map[string]any)
		byName[proxy["name"].(string)] = proxy
	}
	preferred := byName["VMess Argo"]
	if preferred == nil {
		t.Fatalf("argo node missing: %#v", byName)
	}
	if preferred["server"] != "preferred.example.net" || preferred["port"] != 443 || preferred["tls"] != true || preferred["servername"] != "tunnel.example.com" {
		t.Fatalf("preferred argo node = %#v", preferred)
	}
	ws := preferred["ws-opts"].(map[string]any)
	if ws["path"] != "/argo" || ws["headers"].(map[string]any)["Host"] != "tunnel.example.com" {
		t.Fatalf("ws opts = %#v", ws)
	}
	if len(byName) != 2 { // 未就绪与重复协议的隧道都不该再产出节点
		t.Fatalf("names = %#v", byName)
	}
	// 原生节点必须保留在最前。
	if proxies[0].(map[string]any)["name"] != "VMess" {
		t.Fatalf("native node = %#v", proxies[0])
	}
}
