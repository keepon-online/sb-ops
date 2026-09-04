// Package solusvm 拉取 SolusVM 1 客户端 API 的带宽计量。VPS 提供商在面板侧
// 按账单口径计量月流量（含全部网卡流量与宿主层开销），是比本机 V2Ray stats
// 或网卡计数器更权威的月度预算数据源；RackNerd 的 nerdvm 面板即是 SolusVM 1。
//
// 契约（2026-09 实测 nerdvm.racknerd.com）：POST 表单 action=info&bw=true，
// 响应是拼接的 XML 风格标签（无根元素，非标准 XML）；<bw> 四元组单位为字节、
// 顺序为 总量,已用,剩余,百分比整数——2TB 套餐实测 总量=2147483648000（2000GiB）、
// 剩余=总量-已用 精确闭合。<status> 非 success 时失败原因在 <statusmsg>。
// info 属文档外 action（官方 client API 文档只承诺开关机类），解析须宽容、
// 持续失败要有告警兜底。
package solusvm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// requestTimeout 单次 API 请求超时，对齐拨测单协议 10s 的既有取值。
const requestTimeout = 10 * time.Second

// PollInterval 轮询周期：带宽总量变化缓慢且提供商侧有请求成本，小时级足够。
const PollInterval = time.Hour

// Client 调用 SolusVM 1 客户端 API（per-VPS key/hash 鉴权）。注意该凭据
// 同时具备重启/关机这台 VPS 的权限，按敏感凭据对待（config.yaml 0600、不入库）。
type Client struct {
	endpoint string
	key      string
	hash     string
	http     *http.Client
}

func NewClient(endpoint, key, hash string) *Client {
	return &Client{endpoint: endpoint, key: key, hash: hash, http: &http.Client{Timeout: requestTimeout}}
}

// Sample 一次账单口径的月流量采样。
type Sample struct {
	TotalBytes int64
	UsedBytes  int64
	FreeBytes  int64
	// Percent 用 已用/总量 现算，比 API 返回的整数百分比精确。
	Percent float64
	At      time.Time
}

// Info 拉取并解析一次 info 响应。凭据经 POST 表单提交，避免进面板访问日志。
func (c *Client) Info(ctx context.Context) (Sample, error) {
	form := url.Values{}
	form.Set("key", c.key)
	form.Set("hash", c.hash)
	form.Set("action", "info")
	form.Set("bw", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Sample{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return Sample{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Sample{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Sample{}, fmt.Errorf("SolusVM API 返回 HTTP %d", resp.StatusCode)
	}
	return ParseInfo(string(body), time.Now().UTC())
}

// ParseInfo 解析 info 响应为带宽采样。
func ParseInfo(body string, at time.Time) (Sample, error) {
	status := ExtractTag(body, "status")
	if status != "success" {
		message := ExtractTag(body, "statusmsg")
		if message == "" {
			message = status
		}
		return Sample{}, fmt.Errorf("SolusVM API 调用失败：%s", message)
	}
	bw := ExtractTag(body, "bw")
	if bw == "" {
		return Sample{}, errors.New("SolusVM API 响应缺少 bw 字段")
	}
	parts := strings.Split(strings.TrimSpace(bw), ",")
	if len(parts) != 4 {
		return Sample{}, fmt.Errorf("SolusVM API bw 字段格式异常：%q", bw)
	}
	values := make([]int64, 4)
	for i, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil {
			return Sample{}, fmt.Errorf("SolusVM API bw 字段格式异常：%q", bw)
		}
		values[i] = value
	}
	sample := Sample{TotalBytes: values[0], UsedBytes: values[1], FreeBytes: values[2], At: at}
	if sample.TotalBytes > 0 {
		sample.Percent = float64(sample.UsedBytes) / float64(sample.TotalBytes) * 100
	}
	return sample, nil
}

// ExtractTag 取第一个 <tag>value</tag> 的 value；缺失返回空串。
func ExtractTag(body, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(body[start:], close)
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

// State 保存最近一次采样与失败状态，供告警评估与总览读取。纯进程内状态：
// 采样无历史价值（用量随账单周期由提供商重置），重启后首轮轮询即恢复。
type State struct {
	mu          sync.RWMutex
	sample      *Sample
	lastError   string
	lastSuccess time.Time
}

func NewState() *State { return &State{} }

// Snapshot 是 State 的值拷贝视图：Sample 为 nil 表示尚无成功采样。
type Snapshot struct {
	Sample      *Sample
	LastError   string
	LastSuccess time.Time
}

func (s *State) Record(sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := sample
	s.sample = &value
	s.lastError = ""
	s.lastSuccess = sample.At
}

func (s *State) RecordError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := Snapshot{LastError: s.lastError, LastSuccess: s.lastSuccess}
	if s.sample != nil {
		value := *s.sample
		snapshot.Sample = &value
	}
	return snapshot
}
