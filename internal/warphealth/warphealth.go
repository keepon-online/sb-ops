// Package warphealth 周期探测 WARP MASQUE 解锁链路的健康度：经面板 warp-masque
// socks 出站指向的本地 WARP 代理（官方 cloudflare-warp 客户端 proxy 模式），
// 先校验 trace 的 warp=on 与出口族，再以不跟随重定向的 GET 访问 google.com——
// 跳转 google.com.hk 即出口被 Google 判为 CN 区。照 egressgeo 先例：纯逻辑 +
// 闭包注入 + 内存 state，无表无迁移，重启首轮即恢复；门控（出站是否存在）
// 由装配层闭包决定，未部署该链路的实例完全静默。
package warphealth

import (
	"context"
	"crypto/tls"
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
	maxBodySize       = 1 << 10
	// StaleAfter 是「最近尝试距最近一次成功判定」的告警阈值（≈连续 3 轮
	// 探测失败）：隧道黑洞等歧义失败不改变判定，持续失败超过阈值即视为
	// 链路 degraded（照 backup_stalled 的 stale 语义），防粘滞掩盖真实故障。
	StaleAfter = 45 * time.Minute
	// RecentWindow / RecentFailThreshold 是间歇性失败的判定窗口：最近
	// RecentWindow 轮探测中失败 ≥ RecentFailThreshold 轮即视为链路
	// degraded。纯「连续失败」的 StaleAfter 对时通时断的半死形态失明——
	// 2026-10-21 线上实例：warp-svc 半死（MASQUE 隧道任务反复死、SOCKS
	// 监听仍在），5.5 小时 15 轮探测失败穿插 6 轮成功，每次成功都刷新
	// CheckedAt 把 Stale 计时清零，告警从未触发。
	RecentWindow        = 8
	RecentFailThreshold = 6
)

// 单次探测的读超时上限。用 var 仅为测试可调（黑洞隧道用例不必烧满上限）。
var probeTimeout = 15 * time.Second

// 探测目标可注入：生产恒为上方常量，测试替换为本地夹具地址（避免夹具
// 流量漏到公网）。TLS 配置同理：生产 nil（默认校验），测试注入跳过
// httptest 自签证书校验。
var (
	activeTraceURL  = TraceURL
	activeGoogleURL = GoogleURL
	activeTLSConfig *tls.Config
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
	Verdict    string `json:"verdict"` // ok / proxy-down / warp-off / misjudged
	Reason     string `json:"reason,omitempty"`
	WarpOn     bool   `json:"warpOn,omitempty"`
	ExitFamily string `json:"exitFamily,omitempty"` // ipv4 / ipv6 / ""
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
	if activeTLSConfig != nil {
		transport.TLSClientConfig = activeTLSConfig.Clone()
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	warpOn, family, err := fetchTrace(ctx, client, activeTraceURL)
	if err != nil {
		return Result{}, err
	}
	if !warpOn {
		return Result{Verdict: VerdictWarpOff, Reason: "WARP 隧道未生效（trace warp=off，客户端可能已断开）", ExitFamily: family}, nil
	}
	verdict, err := checkGoogle(ctx, client, activeGoogleURL)
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
		return "", fmt.Errorf("解锁探测收到意外状态码 %d（Google）", resp.StatusCode)
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
	recent      []bool // 最近 RecentWindow 轮探测是否失败（true=失败，右端最新）
}

func NewState() *State { return &State{} }

// Snapshot 是 State 的值拷贝视图：Verdict 空串表示尚无成功检查。
type Snapshot struct {
	Verdict       string
	Reason        string
	WarpOn        bool
	ExitFamily    string
	CheckedAt     time.Time
	LastAttempt   time.Time
	LastError     string
	RecentSamples int // 窗口内已积累的探测轮数（≤ RecentWindow）
	RecentFails   int // 窗口内失败的轮数
}

func (s *State) Record(result Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.verdict, s.reason, s.warpOn, s.exitFamily = result.Verdict, result.Reason, result.WarpOn, result.ExitFamily
	s.checkedAt, s.lastAttempt, s.lastError = now, now, ""
	s.pushOutcome(false)
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
	s.pushOutcome(true)
}

// pushOutcome 记录一轮探测结果并裁剪到最近 RecentWindow 轮。
func (s *State) pushOutcome(failed bool) {
	s.recent = append(s.recent, failed)
	if len(s.recent) > RecentWindow {
		s.recent = s.recent[len(s.recent)-RecentWindow:]
	}
}

// Reset 回到未检测态：门控关闭（warp-masque 出站被删）后不应残留旧判定
// 继续驱动告警与卡片。
func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdict, s.reason, s.warpOn, s.exitFamily = "", "", false, ""
	s.checkedAt, s.lastAttempt, s.lastError = time.Time{}, time.Time{}, ""
	s.recent = nil
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fails := 0
	for _, failed := range s.recent {
		if failed {
			fails++
		}
	}
	return Snapshot{
		Verdict:       s.verdict,
		Reason:        s.reason,
		WarpOn:        s.warpOn,
		ExitFamily:    s.exitFamily,
		CheckedAt:     s.checkedAt,
		LastAttempt:   s.lastAttempt,
		LastError:     s.lastError,
		RecentSamples: len(s.recent),
		RecentFails:   fails,
	}
}

// Stale 报告「最近尝试距最近一次成功判定」是否超过阈值：歧义失败粘滞
// 判定是防抖设计，但隧道死亡而 SOCKS 监听仍在的形态下探测会永远落在
// 歧义失败里，判定永远不翻转——持续失败超阈值须按 degraded 处理。
// 尚无成功判定（CheckedAt 零值）时不算 stale（存在性由门控负责）。
func (s Snapshot) Stale(threshold time.Duration) bool {
	if s.CheckedAt.IsZero() || s.LastAttempt.IsZero() {
		return false
	}
	return s.LastAttempt.Sub(s.CheckedAt) > threshold
}

// Flapping 报告最近探测窗口内失败是否已达阈值：时通时断的半死链路里
// 间歇成功会不断洗掉 Stale 计时（见 RecentWindow 的线上实例注释），
// 连续失败语义对此失明，须按窗口失败占比兜底告警。
func (s Snapshot) Flapping() bool {
	return s.RecentFails >= RecentFailThreshold
}
