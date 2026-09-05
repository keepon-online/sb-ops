// Package report 组装每日运行报告（纯逻辑 + 数据源闭包注入，照 backup 的
// dir 闭包 / solusvm 的 bandwidth 闭包先例——包本身不 import 业务服务，
// 装配在 internal/app）。报告不落库：调度与发送在 ops_app 的循环里直调
// 复合 notifier（outbox 的 alert_id 有外键，报告没有对应告警行）。
package report

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Sources 是报告所需的数据源闭包集合。单项失败不毁整份报告——对应行
// 标注「获取失败」，其余指标照常输出。
type Sources struct {
	// YesterdayTraffic 返回上一自然日（本地时区）的上行/下行字节数。
	YesterdayTraffic func(ctx context.Context) (upBytes, downBytes int64, err error)
	// ActiveUsers 返回上一自然日有流量的去重用户数。
	ActiveUsers func(ctx context.Context) (users int64, err error)
	// AlertStats 返回当前开放数与上一自然日的新增/恢复告警数。
	AlertStats func(ctx context.Context) (open, openedYesterday, resolvedYesterday int, err error)
	// LatestBackup 返回最新备份的文件名时间戳（unix 秒）与现存份数。
	LatestBackup func() (createdAt int64, count int, err error)
	// BackupScheduleError 返回定时备份最近一轮的错误（空串表示正常）。
	BackupScheduleError func() string
	// Bandwidth 返回 VPS 月流量账单口径（已用/总量字节与百分比）；ok=false
	// 表示未配置或尚无成功采样。
	Bandwidth func() (usedBytes, totalBytes int64, percent float64, ok bool)
}

// Report 是一份组装完成的每日报告；EventID 由报告日派生（同一天多次
// 构建结果相同），作为事件总线的幂等键防面板重复推送。
type Report struct {
	EventID string
	Title   string
	Message string
}

// BuildDaily 以 now（推送时刻）为基准组装上一自然日（本地时区）的报告。
func BuildDaily(ctx context.Context, now time.Time, sources Sources) Report {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.AddDate(0, 0, -1)

	lines := make([]string, 0, 5)
	lines = append(lines, trafficLine(ctx, sources))
	lines = append(lines, activeUsersLine(ctx, sources))
	lines = append(lines, alertLine(ctx, sources))
	lines = append(lines, backupLine(sources))
	lines = append(lines, bandwidthLine(sources))

	return Report{
		EventID: "report-daily-" + yesterday.Format("20060102"),
		Title:   "📊 每日运行报告 " + yesterday.Format("2006-01-02"),
		Message: strings.Join(lines, "\n"),
	}
}

func trafficLine(ctx context.Context, sources Sources) string {
	up, down, err := sources.YesterdayTraffic(ctx)
	if err != nil {
		return "昨日流量：获取失败"
	}
	return fmt.Sprintf("昨日流量：上行 %s · 下行 %s（合计 %s）", FormatBytes(up), FormatBytes(down), FormatBytes(up+down))
}

func activeUsersLine(ctx context.Context, sources Sources) string {
	users, err := sources.ActiveUsers(ctx)
	if err != nil {
		return "活跃用户：获取失败"
	}
	return fmt.Sprintf("活跃用户：%d", users)
}

func alertLine(ctx context.Context, sources Sources) string {
	open, opened, resolved, err := sources.AlertStats(ctx)
	if err != nil {
		return "告警：获取失败"
	}
	return fmt.Sprintf("告警：当前开放 %d · 昨日新增 %d / 恢复 %d", open, opened, resolved)
}

func backupLine(sources Sources) string {
	createdAt, count, err := sources.LatestBackup()
	if err != nil {
		return "备份：获取失败"
	}
	if count == 0 {
		return "备份：暂无快照"
	}
	line := fmt.Sprintf("备份：最近一份 %s（共 %d 份）", time.Unix(createdAt, 0).Format("2006-01-02 15:04"), count)
	if scheduleErr := sources.BackupScheduleError(); scheduleErr != "" {
		return line + " · 定时备份最近失败：" + scheduleErr
	}
	return line + " · 定时备份正常"
}

func bandwidthLine(sources Sources) string {
	used, total, percent, ok := sources.Bandwidth()
	if !ok {
		return "VPS 月流量：暂无采样"
	}
	return fmt.Sprintf("VPS 月流量：%s / %s（%.1f%%）", FormatBytes(used), FormatBytes(total), percent)
}

// NextRun 返回 now 之后下一个本地时区的 hour 整点；恰在整点触发时顺延
// 到次日，避免零偏移导致的重复调度（与 backup.NextRun 同一模式）。
func NextRun(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// FormatBytes 输出人类可读的字节口径（1024 进制，一位小数）。
func FormatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}
