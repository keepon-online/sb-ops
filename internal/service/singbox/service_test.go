package singbox

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	resource "github.com/keepon-online/sb-ops/internal/service"
	"github.com/keepon-online/sb-ops/internal/service/common"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	_ "github.com/mattn/go-sqlite3"
)

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(resource.Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return NewService(NewStore(db, secret.NewStore(db))), db
}

func TestConfigDefaultsAndDNSDetourReferences(t *testing.T) {
	service, _ := newTestService(t)
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if config.LogLevel != "info" || !config.LogTimestamp || config.DefaultOutboundTag != "direct" || !config.ClashAPIEnabled || !config.V2RayAPIEnabled {
		t.Fatalf("default config = %#v", config)
	}
	_, err = service.UpdateConfig(t.Context(), ConfigWriteInput{
		Revision: config.Revision, LogLevel: "info", LogTimestamp: true,
		DNSServers: []DNSServer{{Tag: "resolver", Address: "https://dns.example/dns-query", Detour: "unknown"}},
		DNSFinal:   "resolver", ClashAPIEnabled: true, ClashAPIListen: "0.0.0.0:9090", V2RayAPIEnabled: true, V2RayAPIListen: "0.0.0.0:10000", DefaultOutboundTag: "direct",
	})
	var validation *common.ValidationError
	if !errors.As(err, &validation) || validation.Fields["dnsServers.0.detour"] == "" {
		t.Fatalf("update config err=%v, want detour validation error", err)
	}
}

func TestClashAPISecretIsEncryptedAndMasked(t *testing.T) {
	service, db := newTestService(t)
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	secretValue := "clash-secret"
	if _, err := service.UpdateConfig(t.Context(), ConfigWriteInput{Revision: config.Revision, LogLevel: config.LogLevel, LogTimestamp: config.LogTimestamp, ClashAPIEnabled: config.ClashAPIEnabled, ClashAPIListen: config.ClashAPIListen, ClashAPISecret: &secretValue, V2RayAPIEnabled: config.V2RayAPIEnabled, V2RayAPIListen: config.V2RayAPIListen, DefaultOutboundTag: config.DefaultOutboundTag}); err != nil {
		t.Fatalf("update config: %v", err)
	}
	value, err := service.GetConfig(t.Context())
	if err != nil || !value.ClashAPISecretSet {
		t.Fatalf("public config = %#v err=%v", value, err)
	}
	var ciphertext []byte
	if err := db.QueryRow(`SELECT clash_api_secret_ciphertext FROM singbox_config WHERE id=1`).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if string(ciphertext) == secretValue {
		t.Fatal("Clash API secret was stored in plaintext")
	}
	plain, err := service.ClashAPISecret(t.Context())
	if err != nil || plain != secretValue {
		t.Fatalf("secret = %q err=%v", plain, err)
	}
}

func TestOutboundSecretsAndReferences(t *testing.T) {
	service, db := newTestService(t)
	password := "upstream-secret"
	outbound, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Tag: "upstream", Server: "127.0.0.1", ServerPort: 1080, Username: "proxy", Password: &password})
	if err != nil {
		t.Fatalf("create outbound: %v", err)
	}
	if !outbound.HasPassword || outbound.Tag != "upstream" {
		t.Fatalf("public outbound = %#v", outbound)
	}
	var ciphertext []byte
	if err := db.QueryRow(`SELECT password_ciphertext FROM singbox_outbounds WHERE id=?`, outbound.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("read encrypted password: %v", err)
	}
	if string(ciphertext) == password {
		t.Fatal("outbound password was stored in plaintext")
	}
	store := NewStore(db, secret.NewStore(db))
	values, passwords, err := store.CompilerOutbounds(t.Context())
	if err != nil || len(values) != 1 || passwords["upstream"] != password {
		t.Fatalf("compiler outbounds values=%#v passwords=%#v err=%v", values, passwords, err)
	}
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	_, err = service.UpdateConfig(t.Context(), ConfigWriteInput{
		Revision: config.Revision, LogLevel: config.LogLevel, LogTimestamp: config.LogTimestamp,
		DNSServers: []DNSServer{{Tag: "resolver", Address: "https://dns.example/dns-query", Detour: "upstream"}}, DNSFinal: "resolver",
		ClashAPIEnabled: config.ClashAPIEnabled, ClashAPIListen: config.ClashAPIListen, V2RayAPIEnabled: config.V2RayAPIEnabled, V2RayAPIListen: config.V2RayAPIListen, DefaultOutboundTag: config.DefaultOutboundTag,
	})
	if err != nil {
		t.Fatalf("reference outbound from DNS: %v", err)
	}
	if err := service.DeleteOutbound(t.Context(), outbound.ID); !errors.Is(err, common.ErrReferenced) {
		t.Fatalf("delete referenced outbound err=%v", err)
	}
}

func TestRuleSetsAndGeoConditions(t *testing.T) {
	service, _ := newTestService(t)
	ruleSet, err := service.CreateRuleSet(t.Context(), RuleSetWriteInput{Tag: "custom-ads", Format: "binary", URL: "https://example.com/ads.srs"})
	if err != nil {
		t.Fatalf("create rule set: %v", err)
	}
	rule, err := service.CreateRule(t.Context(), RouteRuleWriteInput{Name: "block-ads", Enabled: true, Priority: 10, Conditions: RouteConditions{GeoSite: []string{"geosite:category-ads-all"}, GeoIP: []string{"CN"}, RuleSets: []string{"custom-ads"}}, OutboundTag: "block"})
	if err != nil {
		t.Fatalf("create rule with geo/rule-set: %v", err)
	}
	// geo codes are normalized: prefix stripped and lowercased.
	if got := rule.Conditions.GeoSite; len(got) != 1 || got[0] != "category-ads-all" {
		t.Fatalf("normalized geosite = %#v", got)
	}
	if got := rule.Conditions.GeoIP; len(got) != 1 || got[0] != "cn" {
		t.Fatalf("normalized geoip = %#v", got)
	}
	// deleting a referenced rule set is rejected.
	if err := service.DeleteRuleSet(t.Context(), ruleSet.ID); !errors.Is(err, common.ErrReferenced) {
		t.Fatalf("delete referenced rule set err=%v", err)
	}
	// creating a rule referencing an unknown rule set is rejected.
	_, err = service.CreateRule(t.Context(), RouteRuleWriteInput{Name: "bad", Enabled: true, Priority: 20, Conditions: RouteConditions{RuleSets: []string{"missing"}}, OutboundTag: "direct"})
	var validation *common.ValidationError
	if !errors.As(err, &validation) || validation.Fields["conditions.ruleSets"] == "" {
		t.Fatalf("create rule with unknown rule set err=%v", err)
	}
}

func TestOrderRulesAndDryRun(t *testing.T) {
	service, _ := newTestService(t)
	first, err := service.CreateRule(t.Context(), RouteRuleWriteInput{Name: "first", Enabled: true, Priority: 10, Conditions: RouteConditions{Domains: []string{"example.com"}}, OutboundTag: "block"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := service.CreateRule(t.Context(), RouteRuleWriteInput{Name: "second", Enabled: true, Priority: 20, Conditions: RouteConditions{}, OutboundTag: "direct"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if _, err := service.OrderRules(t.Context(), OrderInput{IDs: []string{second.ID, first.ID}}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	rules, err := service.ListRules(t.Context())
	if err != nil || !reflect.DeepEqual([]string{rules[0].ID, rules[1].ID}, []string{second.ID, first.ID}) {
		t.Fatalf("ordered rules=%#v err=%v", rules, err)
	}
	result, err := service.DryRun(t.Context(), DryRunInput{Host: "example.com", Port: 443, Network: "tcp"})
	if err != nil || result.OutboundTag != "direct" || result.MatchedRule == nil || result.MatchedRule.ID != second.ID {
		t.Fatalf("dry run result=%#v err=%v", result, err)
	}
}

func TestLocalHTTPEndpointNormalizesListen(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:9090":   "http://127.0.0.1:9090",
		"0.0.0.0:9090":     "http://127.0.0.1:9090",
		"[::]:9090":        "http://127.0.0.1:9090",
		"localhost:9090":   "http://127.0.0.1:9090",
		"192.168.1.5:9090": "http://192.168.1.5:9090",
	} {
		got, err := localHTTPEndpoint(listen)
		if err != nil {
			t.Fatalf("localHTTPEndpoint(%q): %v", listen, err)
		}
		if got != want {
			t.Fatalf("localHTTPEndpoint(%q)=%q, want %q", listen, got, want)
		}
	}
	if _, err := localHTTPEndpoint("not-an-address"); err == nil {
		t.Fatal("expected error for an invalid listen address")
	}
}

func TestClashAPIEndpointAndSecretGeneration(t *testing.T) {
	service, _ := newTestService(t)

	endpoint, _, enabled, err := service.ClashAPIEndpoint(t.Context())
	if err != nil || !enabled {
		t.Fatalf("ClashAPIEndpoint err=%v enabled=%v", err, enabled)
	}
	if endpoint != "http://127.0.0.1:9090" {
		t.Fatalf("endpoint=%q", endpoint)
	}

	// Clash API is enabled by default, so a secret must be generated once and
	// stay stable across calls.
	first, err := service.EnsureClashAPISecret(t.Context())
	if err != nil || len(first) != 48 {
		t.Fatalf("EnsureClashAPISecret first=%q err=%v", first, err)
	}
	second, err := service.EnsureClashAPISecret(t.Context())
	if err != nil || second != first {
		t.Fatalf("EnsureClashAPISecret second=%q want %q err=%v", second, first, err)
	}
	plain, err := service.ClashAPISecret(t.Context())
	if err != nil || plain != first {
		t.Fatalf("ClashAPISecret=%q want %q err=%v", plain, first, err)
	}

	// Disabling the Clash API disables the endpoint and further generation.
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if _, err := service.UpdateConfig(t.Context(), ConfigWriteInput{
		Revision: config.Revision, LogLevel: config.LogLevel, LogTimestamp: config.LogTimestamp,
		ClashAPIEnabled: false, ClashAPIListen: config.ClashAPIListen, V2RayAPIEnabled: config.V2RayAPIEnabled, V2RayAPIListen: config.V2RayAPIListen, DefaultOutboundTag: config.DefaultOutboundTag,
	}); err != nil {
		t.Fatalf("disable Clash API: %v", err)
	}
	_, _, enabled, err = service.ClashAPIEndpoint(t.Context())
	if err != nil || enabled {
		t.Fatalf("ClashAPIEndpoint after disable err=%v enabled=%v", err, enabled)
	}
	secret, err := service.EnsureClashAPISecret(t.Context())
	if err != nil || secret != "" {
		t.Fatalf("EnsureClashAPISecret after disable=%q err=%v", secret, err)
	}
}

func testWarpKey(char byte) string { return strings.Repeat(string(char), 43) + "=" }

func TestOutboundWarpLifecycle(t *testing.T) {
	service, db := newTestService(t)
	created, err := service.CreateOutbound(t.Context(), OutboundWriteInput{
		Type: OutboundTypeWarp, Tag: "warp",
		Warp: &WarpSettings{PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32", Reserved: []int{1, 2, 3}},
	})
	if err != nil {
		t.Fatalf("create warp outbound: %v", err)
	}
	if created.Warp == nil || created.Warp.PrivateKey != "" || !created.Warp.PrivateKeySet {
		t.Fatalf("created outbound should mask private key: %#v", created.Warp)
	}
	if created.Warp.Endpoint != DefaultWarpEndpoint {
		t.Fatalf("endpoint default = %q", created.Warp.Endpoint)
	}
	var settings []byte
	if err := db.QueryRow(`SELECT settings_json FROM singbox_outbounds WHERE id=?`, created.ID).Scan(&settings); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if bytes.Contains(settings, []byte(testWarpKey('A'))) {
		t.Fatal("WARP private key was stored in plaintext")
	}
	// 留空私钥更新：沿用已存私钥，仅改 reserved。
	updated, err := service.UpdateOutbound(t.Context(), OutboundWriteInput{
		ID: created.ID, Revision: created.Revision, Type: OutboundTypeWarp, Tag: "warp",
		Warp: &WarpSettings{PublicKey: testWarpKey('B'), IPv4: "172.16.0.2/32", Reserved: []int{9, 9, 9}},
	})
	if err != nil {
		t.Fatalf("update warp outbound: %v", err)
	}
	if updated.Warp == nil || updated.Warp.PrivateKey != "" || !updated.Warp.PrivateKeySet {
		t.Fatalf("updated outbound should mask private key: %#v", updated.Warp)
	}
	// 编译路径取回明文私钥。
	outbounds, _, err := NewStore(db, secret.NewStore(db)).CompilerOutbounds(t.Context())
	if err != nil {
		t.Fatalf("compiler outbounds: %v", err)
	}
	found := false
	for _, outbound := range outbounds {
		if outbound.Tag != "warp" {
			continue
		}
		found = true
		if outbound.Warp == nil || outbound.Warp.PrivateKey != testWarpKey('A') {
			t.Fatalf("compiler outbound lost private key: %#v", outbound.Warp)
		}
		if !reflect.DeepEqual(outbound.Warp.Reserved, []int{9, 9, 9}) {
			t.Fatalf("reserved = %#v", outbound.Warp.Reserved)
		}
	}
	if !found {
		t.Fatal("warp outbound missing from compiler outbounds")
	}
	// 类型不可变更。
	_, err = service.UpdateOutbound(t.Context(), OutboundWriteInput{ID: created.ID, Revision: updated.Revision, Type: OutboundTypeSOCKS, Tag: "warp", Server: "1.2.3.4", ServerPort: 1080})
	var validationErr *common.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Fields["type"] == "" {
		t.Fatalf("type change err = %v, want validation error", err)
	}
}

func TestOutboundWarpValidation(t *testing.T) {
	service, _ := newTestService(t)
	cases := []struct {
		name   string
		warp   *WarpSettings
		fields []string
	}{
		{"missing settings", nil, []string{"warp"}},
		{"bad endpoint", &WarpSettings{Endpoint: "no-port", PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32"}, []string{"warp.endpoint"}},
		{"bad public key", &WarpSettings{PublicKey: "short", PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32"}, []string{"warp.publicKey"}},
		{"bad ipv4", &WarpSettings{PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "not-a-cidr"}, []string{"warp.ipv4"}},
		{"bad reserved length", &WarpSettings{PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32", Reserved: []int{1}}, []string{"warp.reserved"}},
		{"bad mtu", &WarpSettings{PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32", MTU: 100}, []string{"warp.mtu"}},
	}
	for _, testCase := range cases {
		_, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: OutboundTypeWarp, Tag: "warp-" + testCase.name, Warp: testCase.warp})
		var validationErr *common.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("%s: err = %v, want validation error", testCase.name, err)
		}
		for _, field := range testCase.fields {
			if validationErr.Fields[field] == "" {
				t.Fatalf("%s: fields = %#v, want %q", testCase.name, validationErr.Fields, field)
			}
		}
	}
	if _, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: "grpc", Tag: "bad-type", Server: "1.2.3.4", ServerPort: 1080}); err == nil {
		t.Fatal("unknown outbound type unexpectedly accepted")
	}
	// 缺少私钥的 WARP 出站应被拒绝。
	if _, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: OutboundTypeWarp, Tag: "no-key", Warp: &WarpSettings{PublicKey: testWarpKey('B'), IPv4: "172.16.0.2/32"}}); err == nil {
		t.Fatal("warp outbound without private key unexpectedly accepted")
	}
}

// 规则集的 download_detour 必须指向存在的出站，且被引用的出站不允许删除：
// 悬空的下载出站只在内核拉取远程规则集时才失败，启动期同步无从暴露。
func TestRuleSetDownloadDetourReferencesOutbound(t *testing.T) {
	service, _ := newTestService(t)
	_, err := service.CreateRuleSet(t.Context(), RuleSetWriteInput{Tag: "openai", Format: "binary", URL: "https://example.com/openai.srs", DownloadDetour: "missing"})
	var fieldError *common.ValidationError
	if !errors.As(err, &fieldError) || fieldError.Fields["downloadDetour"] == "" {
		t.Fatalf("err=%v, want downloadDetour validation error", err)
	}

	password := "secret"
	outbound, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: OutboundTypeSOCKS, Tag: "socks-out", Server: "127.0.0.1", ServerPort: 1080, Password: &password})
	if err != nil {
		t.Fatalf("create outbound: %v", err)
	}
	created, err := service.CreateRuleSet(t.Context(), RuleSetWriteInput{Tag: "openai", Format: "binary", URL: "https://example.com/openai.srs", DownloadDetour: "socks-out"})
	if err != nil {
		t.Fatalf("create rule set with valid detour: %v", err)
	}
	if created.DownloadDetour != "socks-out" {
		t.Fatalf("rule set detour = %q", created.DownloadDetour)
	}
	if err := service.DeleteOutbound(t.Context(), outbound.ID); !errors.Is(err, common.ErrReferenced) {
		t.Fatalf("delete referenced outbound err=%v, want ErrReferenced", err)
	}
	// 改掉 detour 后删除即可通过。
	updated, err := service.UpdateRuleSet(t.Context(), RuleSetWriteInput{ID: created.ID, Revision: created.Revision, Tag: "openai", Format: "binary", URL: "https://example.com/openai.srs"})
	if err != nil {
		t.Fatalf("clear detour: %v", err)
	}
	_ = updated
	if err := service.DeleteOutbound(t.Context(), outbound.ID); err != nil {
		t.Fatalf("delete outbound after clearing detour: %v", err)
	}
}
