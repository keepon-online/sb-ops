package monitor

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// fakeSource 提供可控的原始计数器；errs 按方法名注入失败。
type fakeSource struct {
	host     host.InfoStat
	infos    []cpu.InfoStat
	logical  int
	physical int
	times    []cpu.TimesStat
	vmem     mem.VirtualMemoryStat
	swap     mem.SwapMemoryStat
	usage    disk.UsageStat
	nics     []net.IOCountersStat
	load     load.AvgStat
	errs     map[string]error
}

func (f *fakeSource) fail(name string) error {
	if f.errs == nil {
		return nil
	}
	return f.errs[name]
}

func (f *fakeSource) hostInfo() (*host.InfoStat, error) {
	if err := f.fail("host"); err != nil {
		return nil, err
	}
	value := f.host
	return &value, nil
}

func (f *fakeSource) cpuInfo() ([]cpu.InfoStat, error) {
	if err := f.fail("cpuInfo"); err != nil {
		return nil, err
	}
	return f.infos, nil
}

func (f *fakeSource) cpuCounts() (int, int, error) {
	if err := f.fail("cpuCounts"); err != nil {
		return 0, 0, err
	}
	return f.logical, f.physical, nil
}

func (f *fakeSource) cpuTimes() ([]cpu.TimesStat, error) {
	if err := f.fail("cpuTimes"); err != nil {
		return nil, err
	}
	return f.times, nil
}

func (f *fakeSource) virtualMemory() (*mem.VirtualMemoryStat, error) {
	if err := f.fail("vmem"); err != nil {
		return nil, err
	}
	value := f.vmem
	return &value, nil
}

func (f *fakeSource) swapMemory() (*mem.SwapMemoryStat, error) {
	if err := f.fail("swap"); err != nil {
		return nil, err
	}
	value := f.swap
	return &value, nil
}

func (f *fakeSource) diskUsage() (*disk.UsageStat, error) {
	if err := f.fail("disk"); err != nil {
		return nil, err
	}
	value := f.usage
	return &value, nil
}

func (f *fakeSource) netIOCounters() ([]net.IOCountersStat, error) {
	if err := f.fail("net"); err != nil {
		return nil, err
	}
	return f.nics, nil
}

func (f *fakeSource) loadAvg() (*load.AvgStat, error) {
	if err := f.fail("load"); err != nil {
		return nil, err
	}
	value := f.load
	return &value, nil
}

func newTestService(now func() time.Time, src source) *Service {
	return &Service{now: now, source: src}
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		host: host.InfoStat{
			Hostname: "vps-1", OS: "linux", Platform: "Ubuntu", PlatformVersion: "24.04",
			KernelVersion: "6.8.0", KernelArch: "x86_64", VirtualizationSystem: "kvm",
			BootTime: 1700000000, Uptime: 86400,
		},
		infos:   []cpu.InfoStat{{ModelName: "EPYC 9654"}},
		logical: 2, physical: 1,
		times: []cpu.TimesStat{
			{CPU: "cpu0", User: 100, Idle: 100},
			{CPU: "cpu1", User: 100, Idle: 100},
		},
		vmem: mem.VirtualMemoryStat{Total: 1000, Used: 400, Available: 600, UsedPercent: 40},
		swap: mem.SwapMemoryStat{Total: 500, Used: 100, UsedPercent: 20},
		usage: disk.UsageStat{
			Path: "/", Total: 2000, Used: 800, Free: 1200, UsedPercent: 40,
		},
		nics: []net.IOCountersStat{
			{Name: "eth0", BytesSent: 1000, BytesRecv: 500, PacketsSent: 10, PacketsRecv: 5},
			{Name: "lo", BytesSent: 999999, BytesRecv: 999999},
			{Name: "loopback0", BytesSent: 888888, BytesRecv: 888888},
		},
		load: load.AvgStat{Load1: 0.5, Load5: 0.4, Load15: 0.3},
	}
}

func TestSampleFirstSnapshot(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service := newTestService(func() time.Time { return current }, newFakeSource())

	if err := service.Sample(t.Context()); err != nil {
		t.Fatalf("首次采样不应失败：%v", err)
	}
	value := service.Snapshot()
	if !value.Ready || value.SampledAt != current {
		t.Fatalf("快照应标记就绪并记录采样时间，got ready=%v sampledAt=%v", value.Ready, value.SampledAt)
	}
	if value.Host.Hostname != "vps-1" || value.Host.UptimeSeconds != 86400 || value.Host.BootTime != 1700000000 {
		t.Fatalf("主机信息未正确填充：%+v", value.Host)
	}
	if value.CPU.LogicalCores != 2 || value.CPU.PhysicalCores != 1 || value.CPU.ModelName != "EPYC 9654" {
		t.Fatalf("CPU 静态信息未正确填充：%+v", value.CPU)
	}
	if value.CPU.UsagePercent != 50 || len(value.CPU.PerCore) != 2 || value.CPU.PerCore[0] != 50 {
		t.Fatalf("首次采样（开机均值基线）CPU 占用应为 50%%，got %.2f %+v", value.CPU.UsagePercent, value.CPU.PerCore)
	}
	if value.Memory.UsedPercent != 40 || value.Memory.SwapPercent != 20 || value.Memory.Available != 600 {
		t.Fatalf("内存信息未正确填充：%+v", value.Memory)
	}
	if value.Disk.UsedPercent != 40 || value.Disk.Path != "/" {
		t.Fatalf("磁盘信息未正确填充：%+v", value.Disk)
	}
	if value.Network.SendRate != 0 || value.Network.RecvRate != 0 {
		t.Fatalf("首次采样网络速率应为 0，got %.2f/%.2f", value.Network.SendRate, value.Network.RecvRate)
	}
	if len(value.Network.Interfaces) != 1 || value.Network.Interfaces[0].Name != "eth0" {
		t.Fatalf("应只保留非回环网卡：%+v", value.Network.Interfaces)
	}
	if value.Network.Interfaces[0].BytesSent != 1000 {
		t.Fatalf("网卡累计计数未正确填充：%+v", value.Network.Interfaces[0])
	}
	if len(value.History) != 1 {
		t.Fatalf("首次采样后历史应有 1 个点，got %d", len(value.History))
	}
}

func TestSampleComputesRates(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fake := newFakeSource()
	service := newTestService(func() time.Time { return current }, fake)

	_ = service.Sample(t.Context())

	current = current.Add(5 * time.Second)
	fake.times = []cpu.TimesStat{
		{CPU: "cpu0", User: 110, Idle: 110}, // Δbusy=10, Δtotal=20 → 50%
		{CPU: "cpu1", User: 130, Idle: 110}, // Δbusy=30, Δtotal=40 → 75%
	}
	fake.nics = []net.IOCountersStat{
		{Name: "eth0", BytesSent: 3000, BytesRecv: 2500, PacketsSent: 30, PacketsRecv: 25},
		{Name: "lo", BytesSent: 1000000, BytesRecv: 1000000},
	}
	if err := service.Sample(t.Context()); err != nil {
		t.Fatalf("第二次采样不应失败：%v", err)
	}

	value := service.Snapshot()
	if want := 40.0 / 60.0 * 100; math.Abs(value.CPU.UsagePercent-want) > 0.001 {
		t.Fatalf("总体 CPU 占用应为 %.2f%%，got %.2f", want, value.CPU.UsagePercent)
	}
	if math.Abs(value.CPU.PerCore[0]-50) > 0.001 || math.Abs(value.CPU.PerCore[1]-75) > 0.001 {
		t.Fatalf("每核占用应为 [50 75]，got %+v", value.CPU.PerCore)
	}
	item := value.Network.Interfaces[0]
	if item.SendRate != 400 || item.RecvRate != 400 {
		t.Fatalf("eth0 速率应为 400/400 B/s，got %.1f/%.1f", item.SendRate, item.RecvRate)
	}
	if value.Network.SendRate != 400 || value.Network.RecvRate != 400 {
		t.Fatalf("汇总速率应为 400/400 B/s，got %.1f/%.1f", value.Network.SendRate, value.Network.RecvRate)
	}
	if len(value.History) != 2 {
		t.Fatalf("两次采样后历史应有 2 个点，got %d", len(value.History))
	}
	if value.History[1].CPUPercent != value.CPU.UsagePercent || value.History[1].NetRecvRate != 400 {
		t.Fatalf("历史点未记录最新速率与占用：%+v", value.History[1])
	}
}

func TestSampleCounterRewind(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fake := newFakeSource()
	service := newTestService(func() time.Time { return current }, fake)
	_ = service.Sample(t.Context())

	current = current.Add(5 * time.Second)
	fake.nics = []net.IOCountersStat{{Name: "eth0", BytesSent: 500, BytesRecv: 100}} // 计数器回绕
	_ = service.Sample(t.Context())

	if value := service.Snapshot(); value.Network.SendRate != 0 || value.Network.RecvRate != 0 {
		t.Fatalf("计数器回绕时速率应记 0，got %.1f/%.1f", value.Network.SendRate, value.Network.RecvRate)
	}
}

func TestHistoryRingLimit(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fake := newFakeSource()
	service := newTestService(func() time.Time { return current }, fake)
	for i := 0; i < historyLimit+10; i++ {
		if err := service.Sample(t.Context()); err != nil {
			t.Fatalf("第 %d 次采样失败：%v", i, err)
		}
		current = current.Add(sampleInterval)
	}
	value := service.Snapshot()
	if len(value.History) != historyLimit {
		t.Fatalf("历史应封顶 %d 个点，got %d", historyLimit, len(value.History))
	}
	if !value.History[0].Time.Equal(current.Add(-historyLimit * sampleInterval)) {
		t.Fatalf("最旧历史点应为第 11 次采样：%v", value.History[0].Time)
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service := newTestService(func() time.Time { return current }, newFakeSource())
	_ = service.Sample(t.Context())

	value := service.Snapshot()
	value.History[0].CPUPercent = 123
	value.CPU.PerCore[0] = 123
	value.Network.Interfaces[0].Name = "tampered"

	again := service.Snapshot()
	if again.History[0].CPUPercent == 123 || again.CPU.PerCore[0] == 123 || again.Network.Interfaces[0].Name == "tampered" {
		t.Fatal("Snapshot 返回值必须与内部状态隔离")
	}
}

func TestSamplePartialFailure(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	fake := newFakeSource()
	fake.errs = map[string]error{
		"disk": errors.New("disk unavailable"),
		"load": errors.New("load unavailable"),
	}
	service := newTestService(func() time.Time { return current }, fake)

	err := service.Sample(t.Context())
	if err == nil {
		t.Fatal("部分数据源失败时 Sample 应返回聚合错误")
	}
	value := service.Snapshot()
	if !value.Ready {
		t.Fatal("部分失败仍应产出可用快照")
	}
	if value.Disk.Total != 0 {
		t.Fatalf("失败的磁盘源应置零，got %+v", value.Disk)
	}
	if value.CPU.Load1 != 0 {
		t.Fatalf("失败的负载源应置零，got %+v", value.CPU)
	}
	if value.Memory.UsedPercent != 40 || value.Host.Hostname != "vps-1" {
		t.Fatal("未失败的数据源应正常填充")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	current := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	service := newTestService(func() time.Time { return current }, newFakeSource())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后 Run 应返回 context.Canceled，got %v", err)
	}
	if !service.Snapshot().Ready {
		t.Fatal("Run 启动时应立即完成首次采样")
	}
}
