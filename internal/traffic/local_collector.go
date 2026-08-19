package traffic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	v2rayapi "github.com/sagernet/sing-box/experimental/v2rayapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// LocalNodeID is the fixed identifier for samples collected from the embedded
// sing-box runtime.
const LocalNodeID = "local"

// CollectorConfig selects the local embedded sing-box stats source. The V2Ray
// stats gRPC API carries per-user attribution, so it is the only traffic
// source: inbound users are named by device id, samples are attributed to
// (user, device) and drive both the user quota and per-device usage.
type CollectorConfig struct {
	Interval time.Duration // collection cadence; defaults to 1m
	OnError  func(error)
	// V2RayResolver, when set, resolves the V2Ray stats gRPC address from the
	// panel-managed sing-box configuration on every collection cycle.
	V2RayResolver func(context.Context) (string, bool)
	// InboundResolver maps a sing-box inbound tag back to a protocol ID so the
	// per-inbound stats can be attributed to a protocol.
	InboundResolver func(context.Context, string) (string, bool)
	// DeviceResolver loads the deviceID→userID attribution map once per cycle.
	// Stats for unknown device ids (deleted devices' tail counters) are dropped.
	DeviceResolver func(context.Context) (map[string]string, error)
}

// LocalCollector polls the embedded sing-box runtime and ingests samples directly.
type LocalCollector struct {
	service  *Service
	config   CollectorConfig
	bootID   string
	v2ray    *v2rayCollector
	sequence int64
}

func NewLocalCollector(service *Service, config CollectorConfig) *LocalCollector {
	interval := config.Interval
	if interval < 5*time.Second {
		interval = time.Minute
	}
	config.Interval = interval
	collector := &LocalCollector{service: service, config: config, bootID: uuid.NewString()}
	if config.V2RayResolver != nil {
		collector.v2ray = newV2rayCollector(config.V2RayResolver, config.InboundResolver, config.DeviceResolver)
	}
	return collector
}

// Run polls sing-box at the configured interval until ctx is cancelled.
// Collection errors are retried on the next interval so transient database or
// runtime failures do not stop traffic collection permanently.
func (c *LocalCollector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.collect(ctx); err != nil && c.config.OnError != nil {
				c.config.OnError(err)
			}
		}
	}
}

func (c *LocalCollector) collect(ctx context.Context) error {
	samples, pruned, err := c.samples(ctx)
	if err != nil {
		return err
	}
	if len(pruned.DeviceIDs) > 0 || len(pruned.ProtocolIDs) > 0 {
		if err := c.service.ForgetStats(ctx, pruned.DeviceIDs, pruned.ProtocolIDs); err != nil {
			return fmt.Errorf("清理已删除维度的流量游标: %w", err)
		}
	}
	if len(samples) == 0 {
		c.service.RecordCollection()
		return nil
	}
	c.sequence++
	batch := Batch{
		ID:          uuid.NewString(),
		BootID:      c.bootID,
		Sequence:    c.sequence,
		CollectedAt: time.Now().UTC(),
		Samples:     samples,
	}
	_, err = c.service.IngestLocal(ctx, batch)
	if err != nil {
		return err
	}
	c.service.RecordCollection()
	return nil
}

func (c *LocalCollector) samples(ctx context.Context) ([]Sample, PrunedStats, error) {
	if c.v2ray == nil {
		return nil, PrunedStats{}, errors.New("V2Ray stats API 未配置")
	}
	return c.v2ray.collect(ctx)
}

// --- V2Ray collector: polls the stats gRPC API and accumulates per-user deltas ---

type v2rayStatsQuery func(context.Context, *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error)

const v2rayQueryStatsMethod = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// pruneAbsenceThreshold is how many consecutive polls a dimension may be absent
// from the kernel response before it is dropped from accumulation. The kernel
// stops reporting a counter once the user/protocol is deleted, and an absent
// dimension must leave the batch quickly: its samples would otherwise be
// rejected by dimension validation and stall collection until restart.
const pruneAbsenceThreshold = 3

// PrunedStats lists dimensions removed from accumulation because the kernel
// stopped reporting them. Their cursors must be dropped as well so a future
// re-creation of the same ID re-baselines from zero instead of tripping the
// counter-rollback guard.
type PrunedStats struct {
	DeviceIDs   []string
	ProtocolIDs []string
}

type v2rayCollector struct {
	resolve         func(context.Context) (string, bool)
	inboundResolver func(context.Context, string) (string, bool)
	deviceResolver  func(context.Context) (map[string]string, error)
	query           v2rayStatsQuery
	mu              sync.Mutex
	deviceTotals    map[string]Sample
	protocolTotals  map[string]Sample
	deviceAbsent    map[string]int
	protoAbsent     map[string]int
}

func newV2rayCollector(resolve func(context.Context) (string, bool), inboundResolver func(context.Context, string) (string, bool), deviceResolver func(context.Context) (map[string]string, error)) *v2rayCollector {
	return &v2rayCollector{
		resolve: resolve, inboundResolver: inboundResolver, deviceResolver: deviceResolver,
		deviceTotals: map[string]Sample{}, protocolTotals: map[string]Sample{},
		deviceAbsent: map[string]int{}, protoAbsent: map[string]int{},
	}
}

func (c *v2rayCollector) collect(ctx context.Context) ([]Sample, PrunedStats, error) {
	request := &v2rayapi.QueryStatsRequest{Patterns: []string{"user>>>", "inbound>>>"}, Reset_: true}
	response, err := c.queryStats(ctx, request)
	if err != nil {
		return nil, PrunedStats{}, err
	}
	var deviceUsers map[string]string
	if c.deviceResolver != nil {
		deviceUsers, err = c.deviceResolver(ctx)
		if err != nil {
			return nil, PrunedStats{}, fmt.Errorf("解析设备归属: %w", err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seenDevices := map[string]bool{}
	seenProtocols := map[string]bool{}
	for _, stat := range response.Stat {
		if stat.Value < 0 {
			continue
		}
		if deviceID, direction, ok := parseV2RayDeviceStat(stat.Name); ok {
			userID, known := deviceUsers[deviceID]
			if !known {
				// 已删除设备的残留计数器：立即清除累积总量与缺席计数，
				// 避免残留值毒化后续批次（validateDimensions 查不到
				// devices 行会拒绝整个批次）。
				delete(c.deviceTotals, deviceID)
				delete(c.deviceAbsent, deviceID)
				continue
			}
			seenDevices[deviceID] = true
			sample := c.deviceTotals[deviceID]
			sample.UserID, sample.DeviceID = userID, deviceID
			if direction == "uplink" {
				sample.UpBytes += stat.Value
			} else {
				sample.DownBytes += stat.Value
			}
			c.deviceTotals[deviceID] = sample
			continue
		}
		if tag, direction, ok := parseV2RayInboundStat(stat.Name); ok {
			protocolID, ok := c.resolveProtocol(ctx, tag)
			if !ok {
				continue
			}
			seenProtocols[protocolID] = true
			sample := c.protocolTotals[protocolID]
			sample.ProtocolID = protocolID
			if direction == "uplink" {
				sample.UpBytes += stat.Value
			} else {
				sample.DownBytes += stat.Value
			}
			c.protocolTotals[protocolID] = sample
		}
	}
	pruned := PrunedStats{}
	pruned.DeviceIDs = c.pruneAbsent(c.deviceTotals, c.deviceAbsent, seenDevices, pruneAbsenceThreshold)
	pruned.ProtocolIDs = c.pruneAbsent(c.protocolTotals, c.protoAbsent, seenProtocols, pruneAbsenceThreshold)
	samples := make([]Sample, 0, len(c.deviceTotals)+len(c.protocolTotals))
	for _, sample := range c.deviceTotals {
		samples = append(samples, sample)
	}
	for _, sample := range c.protocolTotals {
		samples = append(samples, sample)
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].UserID != samples[j].UserID {
			return samples[i].UserID < samples[j].UserID
		}
		if samples[i].DeviceID != samples[j].DeviceID {
			return samples[i].DeviceID < samples[j].DeviceID
		}
		return samples[i].ProtocolID < samples[j].ProtocolID
	})
	return samples, pruned, nil
}

// pruneAbsent removes totals for keys the kernel stopped reporting. A key that
// reappears resets its absence counter; once the threshold is reached the key
// and its absence tracking are deleted.
func (c *v2rayCollector) pruneAbsent(totals map[string]Sample, absent map[string]int, seen map[string]bool, threshold int) []string {
	var removed []string
	for key := range totals {
		if seen[key] {
			delete(absent, key)
			continue
		}
		absent[key]++
		if absent[key] >= threshold {
			delete(totals, key)
			delete(absent, key)
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	return removed
}

func (c *v2rayCollector) resolveProtocol(ctx context.Context, tag string) (string, bool) {
	if c.inboundResolver == nil {
		return "", false
	}
	return c.inboundResolver(ctx, tag)
}

func (c *v2rayCollector) queryStats(ctx context.Context, request *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
	if c.query != nil {
		return c.query(ctx, request)
	}
	address, ok := c.resolve(ctx)
	if !ok {
		return nil, errors.New("V2Ray stats API 未配置")
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("连接 V2Ray stats API: %w", err)
	}
	defer func() { _ = connection.Close() }()
	response := new(v2rayapi.QueryStatsResponse)
	if err := connection.Invoke(dialCtx, v2rayQueryStatsMethod, request, response); err != nil {
		return nil, err
	}
	return response, nil
}

// parseV2RayDeviceStat 解析 `user>>>>{设备ID}>>>traffic>>>{up,down}link` 计数器；
// 入站用户 name 是设备 id（UUID），归因到设备后再经 DeviceResolver 折算到用户。
func parseV2RayDeviceStat(name string) (string, string, bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" || (parts[3] != "uplink" && parts[3] != "downlink") {
		return "", "", false
	}
	if _, err := uuid.Parse(parts[1]); err != nil {
		return "", "", false
	}
	return parts[1], parts[3], true
}

func parseV2RayInboundStat(name string) (string, string, bool) {
	parts := strings.Split(name, ">>>")
	if len(parts) != 4 || parts[0] != "inbound" || parts[2] != "traffic" || (parts[3] != "uplink" && parts[3] != "downlink") {
		return "", "", false
	}
	return parts[1], parts[3], true
}
