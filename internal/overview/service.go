package overview

import (
	"context"
	"database/sql"
	"time"

	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	"github.com/keepon-online/sb-ops/internal/service/common"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
)

type Task struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	Action    string `json:"action,omitempty"`
}

type Connections struct {
	Available bool   `json:"available"`
	Count     int    `json:"count"`
	Reason    string `json:"reason,omitempty"`
}

// Devices 汇总全站接入设备：总数、自动发现数、近 24 小时活跃数（按
// last_seen_at）与平台分布。
type Devices struct {
	Total     int            `json:"total"`
	Auto      int            `json:"auto"`
	ActiveDay int            `json:"activeDay"`
	Platforms map[string]int `json:"platforms"`
}

type Result struct {
	Counts          map[string]int           `json:"counts"`
	Devices         Devices                  `json:"devices"`
	RuntimeStatus   string                   `json:"runtimeStatus"`
	ConfigStatus    string                   `json:"configStatus"`
	KernelStatus    string                   `json:"kernelStatus"`
	RuntimeReason   string                   `json:"runtimeReason,omitempty"`
	Tasks           []Task                   `json:"tasks"`
	ConfigDigest    string                   `json:"configDigest,omitempty"`
	LastAppliedAt   *time.Time               `json:"lastAppliedAt,omitempty"`
	LastFailedAt    *time.Time               `json:"lastFailedAt,omitempty"`
	FailureReason   string                   `json:"failureReason,omitempty"`
	ArtifactCount   int                      `json:"artifactCount"`
	Connections     Connections              `json:"connections"`
	Traffic24h      controltraffic.Summary   `json:"traffic24h"`
	TopUsers        []controltraffic.Ranking `json:"topUsers"`
	LastCollectedAt *time.Time               `json:"lastTrafficCollectedAt,omitempty"`
}

type Service struct {
	db      *sql.DB
	sync    *deployment.SyncService
	traffic *controltraffic.Service
}

func NewService(db *sql.DB, syncers ...*deployment.SyncService) *Service {
	var syncer *deployment.SyncService
	if len(syncers) > 0 {
		syncer = syncers[0]
	}
	return &Service{db: db, sync: syncer}
}

func NewServiceWithTraffic(db *sql.DB, syncer *deployment.SyncService, traffic *controltraffic.Service) *Service {
	return &Service{db: db, sync: syncer, traffic: traffic}
}

func (s *Service) Get(ctx context.Context) (Result, error) {
	result := Result{Counts: map[string]int{}, RuntimeStatus: "not_configured", ConfigStatus: "not_checked", KernelStatus: "not_checked", Tasks: []Task{}, TopUsers: []controltraffic.Ranking{}}
	for _, table := range []string{"users", "protocols", "certificates", "policies", "rules"} {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			return Result{}, err
		}
		result.Counts[table] = count
	}
	dayStart := time.Now().UTC().Add(-24 * time.Hour).Unix()
	if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(1),COALESCE(SUM(CASE WHEN ua_key != '' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN last_seen_at IS NOT NULL AND last_seen_at >= ? THEN 1 ELSE 0 END),0)
FROM devices`, dayStart).Scan(&result.Devices.Total, &result.Devices.Auto, &result.Devices.ActiveDay); err != nil {
		return Result{}, err
	}
	result.Devices.Platforms = map[string]int{}
	platformRows, err := s.db.QueryContext(ctx, `SELECT platform,COUNT(1) FROM devices GROUP BY platform ORDER BY COUNT(1) DESC, platform`)
	if err != nil {
		return Result{}, err
	}
	for platformRows.Next() {
		var platform string
		var count int
		if err := platformRows.Scan(&platform, &count); err != nil {
			platformRows.Close()
			return Result{}, err
		}
		if platform == "" {
			platform = "unknown"
		}
		result.Devices.Platforms[platform] = count
	}
	if err := platformRows.Close(); err != nil {
		return Result{}, err
	}
	var appliedAt, failedAt common.Time
	err = s.db.QueryRowContext(ctx, `SELECT config_digest,last_applied_at,last_failed_at,failure_reason,artifact_count FROM runtime_state WHERE id=1`).Scan(&result.ConfigDigest, &appliedAt, &failedAt, &result.FailureReason, &result.ArtifactCount)
	if err != nil && err != sql.ErrNoRows {
		return Result{}, err
	}
	if err == nil && !appliedAt.IsZero() {
		result.LastAppliedAt = appliedAt.Ptr()
		result.RuntimeStatus = "running"
	}
	if !failedAt.IsZero() {
		result.LastFailedAt = failedAt.Ptr()
		if result.LastAppliedAt == nil || result.LastFailedAt.After(*result.LastAppliedAt) {
			result.RuntimeStatus = "unhealthy"
		}
	}
	if s.sync != nil {
		probe := s.sync.Probe(ctx)
		result.ConfigStatus, result.KernelStatus, result.RuntimeReason = probe.ConfigStatus, probe.KernelStatus, probe.Reason
		connections, err := s.sync.Connections(ctx)
		if err != nil {
			return Result{}, err
		}
		result.Connections = Connections{Available: connections.Available, Count: len(connections.Items), Reason: connections.Reason}
		recentFailure := result.LastFailedAt != nil && (result.LastAppliedAt == nil || result.LastFailedAt.After(*result.LastAppliedAt))
		switch {
		case recentFailure || probe.KernelStatus == "unhealthy":
			result.RuntimeStatus = "unhealthy"
		case probe.ConfigStatus == "not_configured":
			result.RuntimeStatus = "not_configured"
		case probe.KernelStatus == "running":
			result.RuntimeStatus = "running"
		}
	}
	// 流量概览与用户排行都是 [now-24h, now) 滚动窗口内 traffic_hourly 用户行
	// 的求和，与用户页 users.used_bytes 累计计数器口径不同（后者自开号/上次
	// 重置起累加，重置只清计数器、留存清理只删历史），两边数字不一致是预期
	// 行为而非采集丢失。
	if s.traffic != nil {
		to := time.Now().UTC()
		filter := controltraffic.Filter{From: to.Add(-24 * time.Hour), To: to, Interval: "hour"}
		traffic, err := s.traffic.Summary(ctx, filter)
		if err != nil {
			return Result{}, err
		}
		rankings, err := s.traffic.Rankings(ctx, filter, "user", 3)
		if err != nil {
			return Result{}, err
		}
		result.Traffic24h, result.TopUsers = traffic, rankings
		if last := s.traffic.LastCollectedAt(); !last.IsZero() {
			result.LastCollectedAt = &last
		}
	}
	result.Tasks = onboardingTasks(result)
	return result, nil
}

func onboardingTasks(result Result) []Task {
	return []Task{
		{Key: "runtime", Title: "确认服务器运行环境", Completed: result.ConfigStatus == "applied" && result.KernelStatus != "unhealthy", Action: "/singbox"},
		{Key: "protocol", Title: "确认至少一个接入协议", Completed: result.Counts["protocols"] > 0, Action: "/protocols"},
		{Key: "user", Title: "创建订阅用户", Completed: result.Counts["users"] > 0, Action: "/users"},
		{Key: "subscription", Title: "生成可下载订阅", Completed: result.ArtifactCount > 0, Action: "/users"},
	}
}
