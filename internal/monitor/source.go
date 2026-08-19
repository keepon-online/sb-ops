package monitor

import (
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// monitoredPath 是磁盘用量监控的目标分区。单 VPS 部署下数据目录、日志与
// 内核缓存都在根分区，监控根分区即可覆盖。
const monitoredPath = "/"

// source 抽象 gopsutil 的原始读取，便于测试注入假数据源。所有方法都应是无
// 状态即时读取（gopsutil 的 cpu.Percent 依赖包内上一次调用状态，故不用）。
type source interface {
	hostInfo() (*host.InfoStat, error)
	cpuInfo() ([]cpu.InfoStat, error)
	cpuCounts() (logical, physical int, err error)
	cpuTimes() ([]cpu.TimesStat, error) // 每核一行，总量由调用方求和
	virtualMemory() (*mem.VirtualMemoryStat, error)
	swapMemory() (*mem.SwapMemoryStat, error)
	diskUsage() (*disk.UsageStat, error)
	netIOCounters() ([]net.IOCountersStat, error) // 每网卡一行
	loadAvg() (*load.AvgStat, error)
}

type gopsutilSource struct{}

func (gopsutilSource) hostInfo() (*host.InfoStat, error) {
	return host.Info()
}

func (gopsutilSource) cpuInfo() ([]cpu.InfoStat, error) {
	return cpu.Info()
}

func (gopsutilSource) cpuCounts() (int, int, error) {
	logical, err := cpu.Counts(true)
	if err != nil {
		return 0, 0, err
	}
	physical, err := cpu.Counts(false)
	if err != nil {
		return 0, 0, err
	}
	return logical, physical, nil
}

func (gopsutilSource) cpuTimes() ([]cpu.TimesStat, error) {
	return cpu.Times(true)
}

func (gopsutilSource) virtualMemory() (*mem.VirtualMemoryStat, error) {
	return mem.VirtualMemory()
}

func (gopsutilSource) swapMemory() (*mem.SwapMemoryStat, error) {
	return mem.SwapMemory()
}

func (gopsutilSource) diskUsage() (*disk.UsageStat, error) {
	return disk.Usage(monitoredPath)
}

func (gopsutilSource) netIOCounters() ([]net.IOCountersStat, error) {
	return net.IOCounters(true)
}

func (gopsutilSource) loadAvg() (*load.AvgStat, error) {
	return load.Avg()
}
