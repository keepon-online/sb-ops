// Package warphealth 周期探测 WARP MASQUE 解锁链路的健康度：经面板 warp-masque
// socks 出站指向的本地 WARP 代理（官方 cloudflare-warp 客户端 proxy 模式），
// 先校验 trace 的 warp=on 与出口族，再以不跟随重定向的 GET 访问 google.com——
// 跳转 google.com.hk 即出口被 Google 判为 CN 区。照 egressgeo 先例：纯逻辑 +
// 闭包注入 + 内存 state，无表无迁移，重启首轮即恢复；门控（出站是否存在）
// 由装配层闭包决定，未部署该链路的实例完全静默。
package warphealth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// 检测节奏与目标是常量硬编码（照 trafficStallThreshold 先例，勿进 config.yaml）：
// 出口判定与隧道状态漂移是小时～天级，15 分钟粒度足够（告警评估每分钟一轮）；
// 启动延迟避开 warp-svc 与面板的启动竞速，防止首误 proxy-down。
const (
	CheckInterval     = 15 * time.Minute
	CheckInitialDelay = time.Minute
	TraceURL          = "https://www.cloudflare.com/cdn-cgi/trace"
	GoogleURL         = "https://www.google.com/"
	proxyDialTimeout  = 3 * time.Second
	probeTimeout      = 15 * time.Second
	maxBodySize       = 1 << 10
)

// 判定终态；state 的 Verdict 空串表示启动后尚无成功检查。
const (
	VerdictOK        = "ok"
	VerdictProxyDown = "proxy-down"
	VerdictWarpOff   = "warp-off"
	VerdictMisjudged = "misjudged"
)

// Result 是一次探测的终态。只有四种终态会更新 state；超时等歧义失败由调用方
// 粘滞上一个已知判定（RecordUnknown），防止网络抖动误 resolve 告警。
type Result struct {
	Verdict    string // ok / proxy-down / warp-off / misjudged
	Reason     string // 非 ok 时的中文原因（告警与总览卡片展示）
	WarpOn     bool
	ExitFamily string // ipv4 / ipv6 / ""
}

// Check 经 proxyAddr 指向的 SOCKS5 代理完整探测一遍链路。返回非 nil error
// 表示歧义失败（超时、意外状态码等），调用方应粘滞上一判定；Result 携带
// 终态时 error 恒为 nil。
func Check(ctx context.Context, proxyAddr string) (Result, error) {
	// 先直连探测本地代理端口：快速把「代理没起来」与「链路质量问题」区分开，
	// 前者是确定性终态，后者才是需要粘滞的歧义失败。
	conn, err := net.DialTimeout("tcp", proxyAddr, proxyDialTimeout)
	if err != nil {
		return Result{Verdict: VerdictProxyDown, Reason: fmt.Sprintf("WARP 本地代理 %s 不可达（warp-svc 可能未运行）", proxyAddr)}, nil
	}
	_ = conn.Close()

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	transport, err := newSocksTransport(proxyAddr)
	if err != nil {
		return Result{}, err
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	warpOn, family, err := fetchTrace(ctx, client, TraceURL)
	if err != nil {
		return Result{}, err
	}
	if !warpOn {
		return Result{Verdict: VerdictWarpOff, Reason: "WARP 隧道未生效（trace warp=off，客户端可能已断开）", ExitFamily: family}, nil
	}
	verdict, err := checkGoogle(ctx, client, GoogleURL)
	if err != nil {
		return Result{}, err
	}
	result := Result{Verdict: verdict, WarpOn: true, ExitFamily: family}
	if verdict == VerdictMisjudged {
		result.Reason = "WARP 出口被 Google 判定为 CN 区（google.com 跳转 google.com.hk），可在 VPS 执行 warp-cli disconnect && warp-cli connect 换出口"
	}
	return result, nil
}

// newSocksTransport 构造经 SOCKS5 代理拨号的 HTTP 传输（域名经代理远端解析，
// 等价 curl 的 --socks5-hostname）。
func newSocksTransport(proxyAddr string) (*http.Transport, error) {
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	return &http.Transport{DialContext: dialContext(dialer)}, nil
}

func dialContext(dialer proxy.Dialer) func(context.Context, string, string) (net.Conn, error) {
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.Dial(network, address)
	}
}

// fetchTrace 取 Cloudflare trace 并解析 warp 开关与出口族。出口协议族由
// Cloudflare 在 MASQUE 隧道内部决定，IPv4-only VPS 也可能拿到 IPv6 出口。
func fetchTrace(ctx context.Context, client *http.Client, target string) (warpOn bool, family string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, "", err
	}
	resp, err := client.Do(request)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, "", fmt.Errorf("WARP trace 探测收到意外状态码 %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return false, "", err
	}
	fields := parseTraceFields(string(body))
	if fields == nil {
		return false, "", errors.New("WARP trace 响应缺少 warp 字段，无法解析")
	}
	return fields["warp"] == "on", exitFamily(fields["ip"]), nil
}

func parseTraceFields(body string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if key, value, found := strings.Cut(strings.TrimSpace(line), "="); found && key != "" {
			fields[key] = value
		}
	}
	if _, ok := fields["warp"]; !ok {
		return nil
	}
	return fields
}

func exitFamily(ip string) string {
	switch {
	case ip == "":
		return ""
	case strings.Contains(ip, ":"):
		return "ipv6"
	case net.ParseIP(ip) != nil:
		return "ipv4"
	default:
		return ""
	}
}

// checkGoogle 以不跟随重定向的 GET 判定 Google 对 WARP 出口的地区判定。
// **必须用 GET**：HEAD 不触发 google.com 的 CN 区 302（线上实测），用 HEAD
// 会把误判误报为正常。非 .hk 的重定向（如 consent 同意页）视为正常。
func checkGoogle(ctx context.Context, client *http.Client, target string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodySize))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return VerdictOK, nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if location := resp.Header.Get("Location"); location != "" && isHKRedirect(target, location) {
			return VerdictMisjudged, nil
		}
		return VerdictOK, nil
	default:
		return "", fmt.Errorf("Google 解锁探测收到意外状态码 %d", resp.StatusCode)
	}
}

// isHKRedirect 判断重定向目标主机是否落在 google.com.hk（含裸域与子域）。
func isHKRedirect(target, location string) bool {
	base, err := url.Parse(target)
	if err != nil {
		return false
	}
	ref, err := url.Parse(location)
	if err != nil {
		return false
	}
	host := base.ResolveReference(ref).Hostname()
	return host == "google.com.hk" || strings.HasSuffix(host, ".google.com.hk")
}

// State 保存最近一次探测结果，供告警评估与总览读取。纯进程内状态（egressgeo
// State 先例）：翻转历史由告警 open/resolve 行记录，重启后首轮检查即恢复。
// 歧义失败粘滞上一个已知判定；门控关闭（出站被删）时 Reset 回未检测。
type State struct {
	mu          sync.RWMutex
	verdict     string
	reason      string
	warpOn      bool
	exitFamily  string
	checkedAt   time.Time
	lastAttempt time.Time
	lastError   string
}

func NewState() *State { return &State{} }

// Snapshot 是 State 的值拷贝视图：Verdict 空串表示尚无成功检查。
type Snapshot struct {
	Verdict     string
	Reason      string
	WarpOn      bool
	ExitFamily  string
	CheckedAt   time.Time
	LastAttempt time.Time
	LastError   string
}

func (s *State) Record(result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.verdict, s.reason, s.warpOn, s.exitFamily = result.Verdict, result.Reason, result.WarpOn, result.ExitFamily
	s.checkedAt, s.lastAttempt, s.lastError = now, now, ""
}

// RecordUnknown 记录一次歧义失败：不清掉上一个已知判定（防抖），只更新
// 最近尝试与错误文本。
func (s *State) RecordUnknown(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAttempt = time.Now().UTC()
	if err != nil {
		s.lastError = err.Error()
	}
}

// Reset 回到未检测态：门控关闭（warp-masque 出站被删）后不应残留旧判定
// 继续驱动告警与卡片。
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdict, s.reason, s.warpOn, s.exitFamily = "", "", false, ""
	s.checkedAt, s.lastAttempt, s.lastError = time.Time{}, time.Time{}, ""
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Verdict:     s.verdict,
		Reason:      s.reason,
		WarpOn:      s.warpOn,
		ExitFamily:  s.exitFamily,
		CheckedAt:   s.checkedAt,
		LastAttempt: s.lastAttempt,
		LastError:   s.lastError,
	}
}
