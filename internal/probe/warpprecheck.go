package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"

	"github.com/keepon-online/sb-ops/internal/deployment/compiler"
	controlegressgeo "github.com/keepon-online/sb-ops/internal/egressgeo"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

// 预检节奏常量（照拨测先例硬编码）：整体上限覆盖内核启动、WireGuard 握手
// 与两次 HTTP 探测；单次探测经隧道，独立限时。
const (
	warpPrecheckTimeout      = 20 * time.Second
	warpPrecheckProbeTimeout = 8 * time.Second
)

const (
	warpPrecheckTag   = "warp-precheck"
	warpPrecheckInTag = "warp-precheck-in"
	egressInfoURL     = "https://ipinfo.io/json"
)

// WarpPrecheckResult 是注册免费 WARP 身份后的即时出口体检结果：经一次性
// 轻量客户端内核（mixed 入站 + 同身份 wireguard endpoint）实测出口 IP 与
// Google 判定。Verdict=unknown 表示探测未完成（见 Error）；预检未执行时
// 整个对象缺省。
type WarpPrecheckResult struct {
	EgressIP string `json:"egressIp,omitempty"`
	Country  string `json:"country,omitempty"`
	Org      string `json:"org,omitempty"`
	Verdict  string `json:"verdict"`
	Redirect string `json:"redirect,omitempty"`
	Error    string `json:"error,omitempty"`
}

type egressInfo struct {
	IP      string
	Country string
	Org     string
}

// WarpPrechecker 对新注册的 WARP 身份做出口预检，把「保存出站、配好分流
// 之后才发现出口不可用」前移成注册时的决策信息。复用拨测的一次性内核
// 机制与 compiler 的 endpoint 构建（保证与真实出站字段一致）；预检失败
// 不影响注册本身（结果里带 Error 字段）。
type WarpPrechecker struct {
	newBox boxConstructor
	ipInfo func(ctx context.Context, client *http.Client) (egressInfo, error)
}

func NewWarpPrechecker() *WarpPrechecker {
	return &WarpPrechecker{newBox: newKernelBox, ipInfo: fetchEgressInfo}
}

// Check 起一次性客户端内核实测该身份的出口。任何一步失败都返回
// Verdict=unknown + Error，绝不返回错误（注册仍可用）。
func (p *WarpPrechecker) Check(ctx context.Context, settings servicesingbox.WarpSettings) WarpPrecheckResult {
	ctx, cancel := context.WithTimeout(ctx, warpPrecheckTimeout)
	defer cancel()
	result := WarpPrecheckResult{Verdict: "unknown"}

	port, err := freeLoopbackPort()
	if err != nil {
		result.Error = truncateError(fmt.Errorf("分配预检端口: %w", err))
		return result
	}
	config, err := buildWarpPrecheckConfig(settings, port)
	if err != nil {
		result.Error = truncateError(err)
		return result
	}
	content, err := json.Marshal(config)
	if err != nil {
		result.Error = truncateError(fmt.Errorf("序列化预检配置: %w", err))
		return result
	}
	var options option.Options
	if err := options.UnmarshalJSONContext(include.Context(ctx), content); err != nil {
		result.Error = truncateError(fmt.Errorf("解析预检配置: %w", err))
		return result
	}
	instance, err := p.newBox(ctx, options)
	if err != nil {
		result.Error = truncateError(fmt.Errorf("创建预检内核: %w", err))
		return result
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		result.Error = truncateError(fmt.Errorf("启动预检内核: %w", err))
		return result
	}
	defer func() { _ = instance.Close() }()

	client := &http.Client{
		Timeout: warpPrecheckProbeTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}),
		},
		// 不跟随重定向：Google 判定依据就是 302 的目标。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// 出口信息失败不阻断判定（ipinfo 偶发不可达时 Google 判定仍有价值）。
	if info, err := p.ipInfo(ctx, client); err == nil {
		result.EgressIP, result.Country, result.Org = info.IP, info.Country, info.Org
	}
	verdict, redirect, err := controlegressgeo.Check(ctx, client, controlegressgeo.CheckURL)
	if err != nil {
		result.Error = truncateError(fmt.Errorf("经 WARP 出口探测 Google 判定失败: %w", err))
		return result
	}
	result.Verdict, result.Redirect = verdict, redirect
	return result
}

// buildWarpPrecheckConfig 组装一次性客户端配置：mixed 入站 + 该身份的
// wireguard endpoint（compiler 同源构建）+ 入站定向路由，独立 box、不配
// experimental/cache-file/DNS（照拨测先例）。
func buildWarpPrecheckConfig(settings servicesingbox.WarpSettings, port int) (map[string]any, error) {
	endpoint, err := compiler.WarpEndpoint(warpPrecheckTag, &settings)
	if err != nil {
		return nil, fmt.Errorf("构建 WARP 预检 endpoint: %w", err)
	}
	return map[string]any{
		"log":       map[string]any{"disabled": true},
		"inbounds":  []any{map[string]any{"type": "mixed", "tag": warpPrecheckInTag, "listen": "127.0.0.1", "listen_port": port}},
		"endpoints": []any{endpoint},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{
			"rules": []any{map[string]any{"inbound": []string{warpPrecheckInTag}, "outbound": warpPrecheckTag}},
			"final": "direct",
		},
	}, nil
}

func fetchEgressInfo(ctx context.Context, client *http.Client) (egressInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, egressInfoURL, nil)
	if err != nil {
		return egressInfo{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return egressInfo{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<12))
	if err != nil {
		return egressInfo{}, err
	}
	return parseEgressInfo(body)
}

func parseEgressInfo(body []byte) (egressInfo, error) {
	var payload struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
		Org     string `json:"org"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return egressInfo{}, fmt.Errorf("解析出口信息响应: %w", err)
	}
	if payload.IP == "" {
		return egressInfo{}, errors.New("出口信息响应缺少 IP")
	}
	return egressInfo{IP: payload.IP, Country: payload.Country, Org: payload.Org}, nil
}
