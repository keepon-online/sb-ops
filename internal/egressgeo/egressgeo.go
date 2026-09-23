// Package egressgeo 周期探测 Google 对本机出口 IP 的地区判定：被判为 CN 区时
// www.google.com 会 302 到 google.com.hk（Gemini 等亦被地区拦截）。该判定全年
// 周期性漂移、通常数周自愈，监控只做「当前判定 + 翻转告警」，不尝试修复。
package egressgeo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 检测节奏与目标是常量硬编码（照 trafficStallThreshold 先例，勿进 config.yaml）：
// 判定漂移是周级，30 分钟粒度足够（告警评估每分钟一轮）。
const (
	CheckInterval  = 30 * time.Minute
	CheckURL       = "https://www.google.com/"
	requestTimeout = 10 * time.Second
	maxBodySize    = 1 << 10
)

// 判定结果；空串表示启动后尚无成功检查。
const (
	VerdictOK        = "ok"
	VerdictMisjudged = "misjudged"
)

// NewClient 返回探测专用 HTTP 客户端：不跟随重定向——判定依据就是 302 的
// Location 目标（照 solusvm 客户端先例防重定向干扰）。
func NewClient() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Check 对 target 发起一次不跟随重定向的 GET：重定向目标是 google.com.hk
// 即判 misjudged（Google 对 CN 区出口的固定跳转）；200 或其它重定向（如
// consent.google.com 同意页）判 ok。redirect 仅在误判时返回跳转目标（证据展示）。
func Check(ctx context.Context, client *http.Client, target string) (verdict, redirect string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", fmt.Errorf("构造出口判定探测请求失败: %w", err)
	}
	resp, err := client.Do(request)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodySize))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return VerdictOK, "", nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		if location := resp.Header.Get("Location"); location != "" && isHKRedirect(target, location) {
			return VerdictMisjudged, location, nil
		}
		return VerdictOK, "", nil
	default:
		return "", "", fmt.Errorf("出口判定探测收到意外状态码 %d", resp.StatusCode)
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

// State 保存最近一次成功判定与失败状态，供告警评估与总览读取。纯进程内状态
// （solusvm State 先例）：判定无历史价值——翻转历史由告警 open/resolve 行记录，
// 重启后首轮检查即恢复。失败粘滞：网络抖动不清掉上一个已知判定，防止误 resolve。
type State struct {
	mu          sync.RWMutex
	verdict     string
	redirect    string
	checkedAt   time.Time
	lastAttempt time.Time
	lastError   string
}

func NewState() *State { return &State{} }

// Snapshot 是 State 的值拷贝视图：Verdict 空串表示尚无成功检查。
type Snapshot struct {
	Verdict     string
	Redirect    string
	CheckedAt   time.Time
	LastAttempt time.Time
	LastError   string
}

func (s *State) Record(verdict, redirect string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.verdict, s.redirect = verdict, redirect
	s.checkedAt, s.lastAttempt, s.lastError = now, now, ""
}

func (s *State) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAttempt = time.Now().UTC()
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Verdict:     s.verdict,
		Redirect:    s.redirect,
		CheckedAt:   s.checkedAt,
		LastAttempt: s.lastAttempt,
		LastError:   s.lastError,
	}
}
