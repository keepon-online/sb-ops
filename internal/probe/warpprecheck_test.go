package probe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"

	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

func testWarpSettings() servicesingbox.WarpSettings {
	return servicesingbox.WarpSettings{
		Endpoint: "engage.cloudflareclient.com:2408", PublicKey: "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=",
		PrivateKey: "yGXW0eJFX1ODeZzDeTtw37Xw+vJforkd5NglVAFKX3o=", IPv4: "172.16.0.2/32",
		Reserved: []int{26, 105, 116},
	}
}

func TestBuildWarpPrecheckConfigMirrorsCompilerEndpoint(t *testing.T) {
	config, err := buildWarpPrecheckConfig(testWarpSettings(), 18080)
	if err != nil {
		t.Fatalf("构建预检配置: %v", err)
	}
	inbounds := config["inbounds"].([]any)
	inbound := inbounds[0].(map[string]any)
	if inbound["type"] != "mixed" || inbound["listen_port"] != 18080 {
		t.Fatalf("入站 = %#v", inbound)
	}
	endpoints := config["endpoints"].([]any)
	endpoint := endpoints[0].(map[string]any)
	if endpoint["type"] != "wireguard" || endpoint["tag"] != warpPrecheckTag || endpoint["private_key"] == "" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
	peers := endpoint["peers"].([]any)
	peer := peers[0].(map[string]any)
	if peer["address"] != "engage.cloudflareclient.com" || peer["port"] != 2408 {
		t.Fatalf("peer = %#v", peer)
	}
	route := config["route"].(map[string]any)
	rules := route["rules"].([]any)
	rule := rules[0].(map[string]any)
	if rule["outbound"] != warpPrecheckTag {
		t.Fatalf("路由规则应把预检入站定向到 WARP endpoint: %#v", rule)
	}
	if route["final"] != "direct" {
		t.Fatalf("兜底应为 direct: %#v", route)
	}
}

func TestBuildWarpPrecheckConfigRejectsBadEndpoint(t *testing.T) {
	settings := testWarpSettings()
	settings.Endpoint = "not-a-host-port"
	if _, err := buildWarpPrecheckConfig(settings, 18080); err == nil || !strings.Contains(err.Error(), "WARP endpoint") {
		t.Fatalf("期望 endpoint 校验错误，得到 %v", err)
	}
}

func TestWarpPrecheckReportsUnknownOnKernelFailure(t *testing.T) {
	prechecker := &WarpPrechecker{
		newBox: func(context.Context, option.Options) (boxInstance, error) {
			return nil, errors.New("mock kernel failure")
		},
		ipInfo: fetchEgressInfo,
	}
	result := prechecker.Check(context.Background(), testWarpSettings())
	if result.Verdict != "unknown" || result.Error == "" {
		t.Fatalf("内核失败应为 unknown + Error：result = %+v", result)
	}
}

func TestParseEgressInfo(t *testing.T) {
	info, err := parseEgressInfo([]byte(`{"ip":"104.28.233.73","country":"US","org":"AS13335 Cloudflare, Inc.","city":"Santa Clara"}`))
	if err != nil {
		t.Fatalf("解析: %v", err)
	}
	if info.IP != "104.28.233.73" || info.Country != "US" || info.Org != "AS13335 Cloudflare, Inc." {
		t.Fatalf("info = %+v", info)
	}
	if _, err := parseEgressInfo([]byte(`{"country":"US"}`)); err == nil {
		t.Fatalf("缺少 IP 应回错: %v", err)
	}
	if _, err := parseEgressInfo([]byte(`not json`)); err == nil {
		t.Fatal("非法 JSON 应回错")
	}
}
