// 定时数据库备份的调度状态与钟点计算。循环体在 internal/app（照
// runSolusVMMetering / runUpgradeCheckLoop 先例），本文件只承载它需要的
// 可测试纯逻辑与共享快照（backup_failed 告警评估的注入源）。
package backup

import (
	"sync"
	"time"
)

// ScheduleHour 是每日定时备份的钟点：本地时区 04:00（业务低峰）。常量
// 硬编码照 trafficStallThreshold 先例，勿进 config.yaml；Telegram 远推
// 尚未实现（待定），本轮只做本地定时快照。
const ScheduleHour = 4

// SnapshotStaleAfter 是「备份链断裂」的判定阈值：24h 调度间隔 + 1h 余量
// （覆盖 VACUUM INTO 执行时长与调度抖动）。健康稳态下最新快照的年龄不会
// 超过 24h；告警评估在定时循环首跑前（重启后）据此按磁盘事实兜底。
const SnapshotStaleAfter = 25 * time.Hour

// NextRun 返回 now 之后下一个本地时区 04:00；恰在整点触发时顺延到次日，
// 避免零偏移导致的重复调度。
func NextRun(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), ScheduleHour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// ScheduleState 记录定时备份循环的最近结果。Snapshot 的 attempted=false
// 表示尚未跑过首轮（面板在首个 04:00 之前），告警评估据此不产生候选。
type ScheduleState struct {
	mu          sync.Mutex
	lastSuccess time.Time
	lastError   string
	attempts    int
}

func NewScheduleState() *ScheduleState {
	return &ScheduleState{}
}

func (s *ScheduleState) RecordSuccess(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSuccess, s.lastError = at, ""
	s.attempts++
}

func (s *ScheduleState) RecordFailure(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = err.Error()
	s.attempts++
}

// Snapshot 返回最近一次成功时间、最近一次错误（空串表示最近一轮成功）与
// 是否已尝试过定时轮次。
func (s *ScheduleState) Snapshot() (lastSuccess time.Time, lastError string, attempted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSuccess, s.lastError, s.attempts > 0
}
