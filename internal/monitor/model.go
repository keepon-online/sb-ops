// Package monitor 周期采集主机级指标（CPU / 内存 / 磁盘 / 网络），维护最近一段
// 时间的采样历史，供管理面板「服务器监控」页面展示。它是无数据库的纯内存服务。
package monitor

import "time"

// Snapshot 是一次完整采样的指标快照；JSON 结构是对外 API 契约
// （docs/api/openapi.management.yaml 的 ManagementMonitorStatus）。
type Snapshot struct {
	Ready     bool           `json:"ready"`
	SampledAt time.Time      `json:"sampledAt"`
	Host      HostInfo       `json:"host"`
	CPU       CPUStats       `json:"cpu"`
	Memory    MemoryStats    `json:"memory"`
	Disk      DiskStats      `json:"disk"`
	Network   NetworkStats   `json:"network"`
	History   []HistoryPoint `json:"history"`
}

// HostInfo 描述主机静态信息与运行时长。
type HostInfo struct {
	Hostname             string `json:"hostname"`
	OS                   string `json:"os"`
	Platform             string `json:"platform"`
	PlatformVersion      string `json:"platformVersion"`
	KernelVersion        string `json:"kernelVersion"`
	KernelArch           string `json:"kernelArch"`
	VirtualizationSystem string `json:"virtualizationSystem"`
	BootTime             int64  `json:"bootTime"`      // Unix 秒
	UptimeSeconds        uint64 `json:"uptimeSeconds"` // 自本次开机起
}

// CPUStats 描述处理器占用与负载；usagePercent 为全部逻辑核的总体占用。
type CPUStats struct {
	LogicalCores  int       `json:"logicalCores"`
	PhysicalCores int       `json:"physicalCores"`
	ModelName     string    `json:"modelName"`
	UsagePercent  float64   `json:"usagePercent"`
	PerCore       []float64 `json:"perCore"`
	Load1         float64   `json:"load1"`
	Load5         float64   `json:"load5"`
	Load15        float64   `json:"load15"`
}

// MemoryStats 描述物理内存与交换分区用量，字节数。
type MemoryStats struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"usedPercent"`
	SwapTotal   uint64  `json:"swapTotal"`
	SwapUsed    uint64  `json:"swapUsed"`
	SwapPercent float64 `json:"swapPercent"`
}

// DiskStats 描述根分区用量，字节数。
type DiskStats struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
}

// NetworkStats 描述非回环网卡的收发计数与速率（字节/秒）。
type NetworkStats struct {
	SendRate   float64          `json:"sendRate"`
	RecvRate   float64          `json:"recvRate"`
	Interfaces []InterfaceStats `json:"interfaces"`
}

// InterfaceStats 描述单块网卡的累计收发计数与当前速率。
type InterfaceStats struct {
	Name        string  `json:"name"`
	BytesSent   uint64  `json:"bytesSent"`
	BytesRecv   uint64  `json:"bytesRecv"`
	PacketsSent uint64  `json:"packetsSent"`
	PacketsRecv uint64  `json:"packetsRecv"`
	SendRate    float64 `json:"sendRate"`
	RecvRate    float64 `json:"recvRate"`
}

// HistoryPoint 是趋势图的一个采样点；速率与占用均为采样间隔内的均值。
type HistoryPoint struct {
	Time          time.Time `json:"time"`
	CPUPercent    float64   `json:"cpuPercent"`
	MemoryPercent float64   `json:"memoryPercent"`
	NetSendRate   float64   `json:"netSendRate"`
	NetRecvRate   float64   `json:"netRecvRate"`
}
