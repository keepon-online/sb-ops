package monitor

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/net"
)

const (
	// sampleInterval 是后台采样周期，与 historyLimit 共同决定趋势窗口
	//（5 秒 × 120 点 = 最近 10 分钟）。
	sampleInterval = 5 * time.Second
	historyLimit   = 120
)

// Service 周期采集主机指标并维护最近一段时间的采样历史。CPU 占用与网络
// 速率由相邻两次采样的原始计数器差值计算，不依赖 gopsutil 的包内状态。
type Service struct {
	mu     sync.RWMutex
	now    func() time.Time
	source source

	latest  Snapshot
	history []HistoryPoint

	prevCPU   []cpu.TimesStat
	prevNet   []net.IOCountersStat
	prevNetAt time.Time

	// CPU 型号与核数在首次读取成功后缓存（内容不会变化）。
	cpuModel      string
	logicalCores  int
	physicalCores int
}

func NewService() *Service {
	return &Service{
		now:    func() time.Time { return time.Now().UTC() },
		source: gopsutilSource{},
	}
}

// Run 阻塞执行周期采样直到 ctx 取消；启动时立即采一次，页面打开时基本
// 已有可用数据。
func (s *Service) Run(ctx context.Context) error {
	_ = s.Sample(ctx)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_ = s.Sample(ctx)
		}
	}
}

// Sample 执行一次采样并更新快照与历史。调用方需保证不并发调用（Run 循环
// 与测试均串行调用）。个别数据源失败只置零对应字段，返回聚合 error 供调用
// 方记录，不影响其余字段与后续采样。
func (s *Service) Sample(_ context.Context) error {
	var errs []error
	now := s.now()

	value := Snapshot{Ready: true, SampledAt: now}

	if info, err := s.source.hostInfo(); err != nil {
		errs = append(errs, err)
	} else {
		value.Host = HostInfo{
			Hostname: info.Hostname, OS: info.OS,
			Platform: info.Platform, PlatformVersion: info.PlatformVersion,
			KernelVersion: info.KernelVersion, KernelArch: info.KernelArch,
			VirtualizationSystem: info.VirtualizationSystem,
			BootTime:             int64(info.BootTime), UptimeSeconds: info.Uptime,
		}
	}

	if s.cpuModel == "" || s.logicalCores == 0 {
		if infos, err := s.source.cpuInfo(); err != nil {
			errs = append(errs, err)
		} else if len(infos) > 0 {
			s.cpuModel = infos[0].ModelName
		}
		if logical, physical, err := s.source.cpuCounts(); err != nil {
			errs = append(errs, err)
		} else {
			s.logicalCores, s.physicalCores = logical, physical
		}
	}
	value.CPU.ModelName = s.cpuModel
	value.CPU.LogicalCores = s.logicalCores
	value.CPU.PhysicalCores = s.physicalCores

	if loadAvg, err := s.source.loadAvg(); err != nil {
		errs = append(errs, err)
	} else {
		value.CPU.Load1, value.CPU.Load5, value.CPU.Load15 = loadAvg.Load1, loadAvg.Load5, loadAvg.Load15
	}

	if vmem, err := s.source.virtualMemory(); err != nil {
		errs = append(errs, err)
	} else {
		value.Memory.Total, value.Memory.Used = vmem.Total, vmem.Used
		value.Memory.Available, value.Memory.UsedPercent = vmem.Available, vmem.UsedPercent
	}
	if swap, err := s.source.swapMemory(); err != nil {
		errs = append(errs, err)
	} else {
		value.Memory.SwapTotal, value.Memory.SwapUsed = swap.Total, swap.Used
		value.Memory.SwapPercent = swap.UsedPercent
	}

	if usage, err := s.source.diskUsage(); err != nil {
		errs = append(errs, err)
	} else {
		value.Disk = DiskStats{
			Path: usage.Path, Total: usage.Total, Used: usage.Used,
			Free: usage.Free, UsedPercent: usage.UsedPercent,
		}
	}

	times, cpuErr := s.source.cpuTimes()
	if cpuErr != nil {
		errs = append(errs, cpuErr)
	} else {
		value.CPU.UsagePercent, value.CPU.PerCore = cpuUsage(s.prevCPU, times)
	}

	counters, netErr := s.source.netIOCounters()
	if netErr != nil {
		errs = append(errs, netErr)
	} else {
		elapsed := 0.0
		if !s.prevNetAt.IsZero() {
			elapsed = now.Sub(s.prevNetAt).Seconds()
		}
		value.Network = networkStats(s.prevNet, counters, elapsed)
	}

	point := HistoryPoint{
		Time:          now,
		CPUPercent:    value.CPU.UsagePercent,
		MemoryPercent: value.Memory.UsedPercent,
		NetSendRate:   value.Network.SendRate,
		NetRecvRate:   value.Network.RecvRate,
	}

	s.mu.Lock()
	s.latest = value
	s.history = append(s.history, point)
	if len(s.history) > historyLimit {
		s.history = s.history[len(s.history)-historyLimit:]
	}
	if cpuErr == nil {
		s.prevCPU = times
	}
	if netErr == nil {
		s.prevNet, s.prevNetAt = counters, now
	}
	s.mu.Unlock()

	return errors.Join(errs...)
}

// Snapshot 返回最新快照与完整历史，深拷贝避免调用方与采样循环共享底层数组。
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.latest
	value.History = append([]HistoryPoint(nil), s.history...)
	if s.latest.CPU.PerCore != nil {
		value.CPU.PerCore = append([]float64(nil), s.latest.CPU.PerCore...)
	}
	if s.latest.Network.Interfaces != nil {
		value.Network.Interfaces = append([]InterfaceStats(nil), s.latest.Network.Interfaces...)
	}
	return value
}

// cpuTotal 汇总计数器全部状态的时间；gopsutil 的 TimesStat.Total 已废弃，
// 这里手工求和（Guest 在 Linux 上计入 User，重不重复计入不影响差值结果）。
func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal + t.Guest + t.GuestNice
}

// cpuBusy 返回扣除空闲后的忙碌时间。
func cpuBusy(t cpu.TimesStat) float64 {
	return cpuTotal(t) - t.Idle - t.Iowait
}

func percentDelta(prevBusy, prevTotal, curBusy, curTotal float64) float64 {
	totalDelta := curTotal - prevTotal
	if totalDelta <= 0 {
		return 0
	}
	busyDelta := curBusy - prevBusy
	if busyDelta < 0 {
		busyDelta = 0
	}
	return math.Min(100, math.Max(0, busyDelta/totalDelta*100))
}

// cpuUsage 由相邻两轮每核计数器差值计算总体与每核占用；prev 为空（首次采
// 样）时以零值基线计算，结果等于开机以来的平均占用。
func cpuUsage(prev, current []cpu.TimesStat) (float64, []float64) {
	prevByName := make(map[string]cpu.TimesStat, len(prev))
	var prevBusySum, prevTotalSum float64
	for _, t := range prev {
		prevByName[t.CPU] = t
		prevBusySum += cpuBusy(t)
		prevTotalSum += cpuTotal(t)
	}
	var curBusySum, curTotalSum float64
	perCore := make([]float64, 0, len(current))
	for _, t := range current {
		curBusySum += cpuBusy(t)
		curTotalSum += cpuTotal(t)
		p := prevByName[t.CPU]
		perCore = append(perCore, percentDelta(cpuBusy(p), cpuTotal(p), cpuBusy(t), cpuTotal(t)))
	}
	return percentDelta(prevBusySum, prevTotalSum, curBusySum, curTotalSum), perCore
}

// networkStats 汇总非回环网卡的累计计数与速率；elapsed 非正（首次采样或时
// 钟未推进）时速率记零，计数器回绕（当前值小于上次）同样记零。
func networkStats(prev, current []net.IOCountersStat, elapsed float64) NetworkStats {
	prevByName := make(map[string]net.IOCountersStat, len(prev))
	for _, nic := range prev {
		prevByName[nic.Name] = nic
	}
	stats := NetworkStats{Interfaces: make([]InterfaceStats, 0, len(current))}
	for _, nic := range current {
		// 常规 Linux 回环为 lo；WSL2 镜像网络下另有 loopback0，本机代理
		// 流量都在其中，一并排除避免虚增速率。
		if nic.Name == "lo" || nic.Name == "loopback0" {
			continue
		}
		item := InterfaceStats{
			Name: nic.Name, BytesSent: nic.BytesSent, BytesRecv: nic.BytesRecv,
			PacketsSent: nic.PacketsSent, PacketsRecv: nic.PacketsRecv,
		}
		if elapsed > 0 {
			if p, ok := prevByName[nic.Name]; ok {
				item.SendRate = counterRate(p.BytesSent, nic.BytesSent, elapsed)
				item.RecvRate = counterRate(p.BytesRecv, nic.BytesRecv, elapsed)
			}
		}
		stats.SendRate += item.SendRate
		stats.RecvRate += item.RecvRate
		stats.Interfaces = append(stats.Interfaces, item)
	}
	return stats
}

func counterRate(prev, current uint64, elapsed float64) float64 {
	if current < prev || elapsed <= 0 {
		return 0
	}
	return float64(current-prev) / elapsed
}
