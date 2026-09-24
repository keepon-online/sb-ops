// Package probe 提供协议端到端拨测：为每个协议在进程内起一个轻量
// sing-box 客户端实例（本地 mixed 入站 + 该协议出站），HTTP 请求经代理
// 穿回本机协议入站、再由主内核路由打探测目标——「端口在听」之外验证
// 「链路真的能通」（Reality 握手、证书校验、认证、转发、出站全链路）。
package probe

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"golang.org/x/sync/errgroup"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

const (
	// ProbeTargetURL 是默认探测目标：经协议隧道与主内核出站打到公网，
	// 同时验证入站链路和服务器出站。
	ProbeTargetURL = "http://www.gstatic.com/generate_204"

	// ProbeInterval 与 ProbeInitialDelay 是周期拨测节奏：启动后等首轮
	// 同步落定再首测，之后固定周期。常量硬编码（照 trafficStallThreshold
	// 先例），不进 config.yaml。
	ProbeInterval     = 5 * time.Minute
	ProbeInitialDelay = 30 * time.Second

	perProbeTimeout  = 10 * time.Second
	probeConcurrency = 4
	errorLimit       = 300
)

// ErrKernelNotReady 表示本轮因内核未运行/配置未应用而跳过——内核故障已有
// runtime_sync_failed 告警覆盖，拨测此时写失败行只会制造误报。
var ErrKernelNotReady = errors.New("内核未运行或配置未应用，跳过拨测")

type boxInstance interface {
	Start() error
	Close() error
}

type boxConstructor func(ctx context.Context, options option.Options) (boxInstance, error)

// Prober 执行端到端拨测并落库最近一次结果。ready 由装配方注入（内核
// running 且配置 applied 才放行）；targetURL/newBox 可在测试中替换。
type Prober struct {
	db        *sql.DB
	store     *Store
	ready     func(ctx context.Context) (bool, string)
	targetURL func() string
	newBox    boxConstructor
	now       func() time.Time
	mu        sync.Mutex // 串行化拨测轮次（周期轮与手动触发互斥）
}

func NewProber(db *sql.DB, ready func(ctx context.Context) (bool, string)) *Prober {
	return NewProberWithTargetURL(db, ready, ProbeTargetURL)
}

func NewProberWithTargetURL(db *sql.DB, ready func(ctx context.Context) (bool, string), targetURL string) *Prober {
	return NewProberWithTargetURLProvider(db, ready, func() string { return targetURL })
}

func NewProberWithTargetURLProvider(db *sql.DB, ready func(ctx context.Context) (bool, string), targetURL func() string) *Prober {
	prober := &Prober{
		db: db, store: NewStore(db), ready: ready,
		targetURL: targetURL, now: func() time.Time { return time.Now().UTC() },
	}
	// 不接 PlatformLogWriter（会强制启用 cache-file 与 clash-server 服务，
	// 见 runtime/singbox 的注释）：拨测实例日志关闭，错误经 HTTP 代理层
	// 报回客户端即可捕获。
	prober.newBox = newKernelBox
	return prober
}

// newKernelBox 起独立轻量客户端内核（拨测与 WARP 出口预检共用）。不接
// PlatformLogWriter（会强制启用 cache-file 与 clash-server 服务）：错误经
// HTTP 代理层报回客户端即可捕获。
func newKernelBox(ctx context.Context, options option.Options) (boxInstance, error) {
	return box.New(box.Options{Context: include.Context(ctx), Options: options})
}

// StoredResults 返回最近一轮落库的拨测结果（读取面，不触发拨测）。
func (p *Prober) StoredResults(ctx context.Context) ([]Item, error) {
	return p.store.List(ctx)
}

// Probe 跑一轮拨测。protocolID 非空时只测该协议；结果逐协议 upsert 落库。
func (p *Prober) Probe(ctx context.Context, protocolID string) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ready != nil {
		if ok, reason := p.ready(ctx); !ok {
			if reason == "" {
				reason = "内核未就绪"
			}
			return Result{}, fmt.Errorf("%w（%s）", ErrKernelNotReady, reason)
		}
	}

	protocols, err := serviceprotocol.NewStore(p.db, secret.NewStore(p.db)).List(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("加载协议列表失败: %w", err)
	}
	users, err := serviceuser.NewStore(p.db).ListArtifactUsers(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("加载用户列表失败: %w", err)
	}
	devices, err := serviceuser.NewStore(p.db).ListArtifactDevices(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("加载设备凭据失败: %w", err)
	}
	assignments, err := loadProtocolAssignments(ctx, p.db)
	if err != nil {
		return Result{}, fmt.Errorf("加载协议分配失败: %w", err)
	}

	now := p.now()
	available := map[string]bool{}
	for _, user := range users {
		available[user.ID] = user.AvailableAt(now)
	}
	// 与编译器同一借用面：设备启用、所属用户可用且被分配该协议。
	borrowable := map[string][]serviceuser.ArtifactDevice{}
	for _, device := range devices {
		for protocolID := range assignments[device.UserID] {
			if device.Enabled && available[device.UserID] {
				borrowable[protocolID] = append(borrowable[protocolID], device)
			}
		}
	}

	checkedAt := p.now()
	targets := make([]serviceprotocol.Protocol, 0, len(protocols))
	for _, protocol := range protocols {
		if protocolID != "" && protocol.ID != protocolID {
			continue
		}
		targets = append(targets, protocol)
	}
	items := make([]Item, len(targets))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(probeConcurrency)
	for index, protocol := range targets {
		items[index] = Item{ProtocolID: protocol.ID, ProtocolName: protocol.Name, CheckedAt: checkedAt}
		if !protocol.Enabled {
			items[index].Status = "skipped"
			items[index].Error = "协议已停用"
			continue
		}
		candidates := borrowable[protocol.ID]
		if len(candidates) == 0 {
			items[index].Status = "skipped"
			items[index].Error = "没有可用的分配用户与设备，无凭据可测"
			continue
		}
		// 取借用面中第一台（按 user_id, device_id 排序，稳定可复现）。
		device := candidates[0]
		group.Go(func() error {
			items[index] = p.probeOne(groupCtx, protocol, device, checkedAt)
			return nil // 单协议失败是数据不是错误，整轮不因此中断
		})
	}
	_ = group.Wait()

	result := Result{CheckedAt: checkedAt, Items: items}
	if err := p.store.Upsert(ctx, items); err != nil {
		slog.Error("拨测结果落库失败", "error", err)
	}
	return result, nil
}

// probeOne 为单个协议起一次性客户端内核并经其代理打探测目标。
func (p *Prober) probeOne(ctx context.Context, protocol serviceprotocol.Protocol, device serviceuser.ArtifactDevice, checkedAt time.Time) Item {
	item := Item{ProtocolID: protocol.ID, ProtocolName: protocol.Name, CheckedAt: checkedAt}
	outbound, err := clientOutbound(protocol, device)
	if err != nil {
		item.Status, item.Error = "failed", truncateError(err)
		return item
	}
	port, err := freeLoopbackPort()
	if err != nil {
		item.Status, item.Error = "failed", truncateError(fmt.Errorf("分配拨测端口: %w", err))
		return item
	}
	config := map[string]any{
		"log": map[string]any{"disabled": true},
		"inbounds": []any{map[string]any{
			"type": "mixed", "tag": "probe-in", "listen": "127.0.0.1", "listen_port": port,
		}},
		"outbounds": []any{outbound, map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{
			"rules": []any{map[string]any{"inbound": []string{"probe-in"}, "outbound": "probe-out"}},
			"final": "direct",
		},
	}
	content, err := json.Marshal(config)
	if err != nil {
		item.Status, item.Error = "failed", truncateError(err)
		return item
	}
	var options option.Options
	if err := options.UnmarshalJSONContext(include.Context(ctx), content); err != nil {
		item.Status, item.Error = "failed", truncateError(fmt.Errorf("解析拨测配置: %w", err))
		return item
	}
	instance, err := p.newBox(ctx, options)
	if err != nil {
		item.Status, item.Error = "failed", truncateError(fmt.Errorf("创建拨测内核: %w", err))
		return item
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		item.Status, item.Error = "failed", truncateError(fmt.Errorf("启动拨测内核: %w", err))
		return item
	}
	defer func() { _ = instance.Close() }()

	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)})},
		Timeout:   perProbeTimeout,
	}
	started := time.Now()
	response, err := client.Get(p.targetURL())
	latency := time.Since(started).Milliseconds()
	if err != nil {
		item.Status, item.Error = "failed", truncateError(err)
		return item
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		item.Status, item.Error = "failed", fmt.Sprintf("探测目标返回 HTTP %d", response.StatusCode)
		return item
	}
	item.Status = "ok"
	item.LatencyMS = &latency
	return item
}

// freeLoopbackPort 预绑环回临时端口再释放交给拨测入站。预绑与内核监听
// 之间存在微小竞窗，但仅本进程、仅环回，撞上时该轮失败、下轮重试。
func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func loadProtocolAssignments(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id,protocol_id FROM user_protocols`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := map[string]map[string]bool{}
	for rows.Next() {
		var userID, protocolID string
		if err := rows.Scan(&userID, &protocolID); err != nil {
			return nil, err
		}
		if assignments[userID] == nil {
			assignments[userID] = map[string]bool{}
		}
		assignments[userID][protocolID] = true
	}
	return assignments, rows.Err()
}

func truncateError(err error) string {
	message := err.Error()
	if len(message) <= errorLimit {
		return message
	}
	return message[:errorLimit] + "…"
}
