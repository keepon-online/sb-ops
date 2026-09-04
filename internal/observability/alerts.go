package observability

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Alert struct {
	ID             string     `json:"id"`
	Fingerprint    string     `json:"fingerprint"`
	Type           string     `json:"type"`
	Severity       string     `json:"severity"`
	ResourceType   string     `json:"resourceType"`
	ResourceID     string     `json:"resourceId"`
	Message        string     `json:"message"`
	Status         string     `json:"status"`
	FirstSeenAt    time.Time  `json:"firstSeenAt"`
	LastSeenAt     time.Time  `json:"lastSeenAt"`
	AcknowledgedAt *time.Time `json:"acknowledgedAt,omitempty"`
	AcknowledgedBy *string    `json:"acknowledgedBy,omitempty"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

type candidate struct{ alertType, severity, resourceType, resourceID, message string }

// BandwidthStatus 是提供商带宽计量的注入视图（solusvm 轮询循环的快照换算），
// 本地引用类型让 observability 不依赖 internal/solusvm（与 UnknownDeviceRef
// 同例）。ok=false 表示未配置该数据源，不产生任何候选。
type BandwidthStatus struct {
	UsedBytes   int64
	TotalBytes  int64
	LastSuccess time.Time
	// Failed 表示最近一轮拉取失败；短暂失败不清既有采样，配合 staleAfter
	// 阈值避免单次抖动直接告警。
	Failed bool
}

const (
	// bandwidthWarnPercent / bandwidthCriticalPercent 是 VPS 月流量预算的
	// 告警阈值：80% 提前量足够人肉干预（提醒重度用户/临时调小配额），95%
	// 视为接近超额。
	bandwidthWarnPercent     = 80
	bandwidthCriticalPercent = 95
	// bandwidthStaleAfter 是计量拉取持续无成功采样的告警阈值：小时级轮询
	// 下即连续 6 轮失败（API 被提供商禁用/凭据失效等），数据源静默失效
	// 必须可见。
	bandwidthStaleAfter = 6 * time.Hour
)

type Service struct {
	db            *sql.DB
	notifier      Notifier
	now           func() time.Time
	expiryWarning func() time.Duration
	maxDBSize     int64
	bandwidth     func() (BandwidthStatus, bool)
	upgrade       func() (UpgradeStatus, bool)
}

func NewService(db *sql.DB, notifier Notifier) *Service {
	return &Service{db: db, notifier: notifier, now: func() time.Time { return time.Now().UTC() }, expiryWarning: func() time.Duration { return 30 * 24 * time.Hour }, maxDBSize: 10 << 30}
}

// SetExpiryWarning injects the live runtime configuration without coupling the
// observability package to the config package.
func (s *Service) SetExpiryWarning(value func() time.Duration) {
	s.expiryWarning = value
}

// SetBandwidthSource 注入提供商带宽计量视图（未注入或返回 ok=false 时不评估）。
func (s *Service) SetBandwidthSource(source func() (BandwidthStatus, bool)) {
	s.bandwidth = source
}

// UpgradeStatus 是面板版本检查的注入视图（升级服务 Status 快照的换算，
// 本地引用类型同 BandwidthStatus 例）。ok=false 表示未配置升级源或尚无
// 检查结果，不产生候选。
type UpgradeStatus struct {
	CurrentVersion string
	LatestVersion  string
	Available      bool
}

// SetUpgradeSource 注入面板版本视图（未注入或返回 ok=false 时不评估）。
func (s *Service) SetUpgradeSource(source func() (UpgradeStatus, bool)) {
	s.upgrade = source
}

func (s *Service) Evaluate(ctx context.Context, aggregationErr error, trafficStalled bool) error {
	now := s.now()
	values := []candidate{}
	for _, query := range []struct{ sql, kind, severity, resource, message string }{
		{`SELECT 'runtime' FROM runtime_state WHERE last_failed_at>=?`, "runtime_sync_failed", "critical", "singbox", "sing-box configuration synchronization failed"},
	} {
		rows, err := s.db.QueryContext(ctx, query.sql, now.Add(-24*time.Hour).Unix())
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			values = append(values, candidate{query.kind, query.severity, query.resource, id, query.message})
		}
		rows.Close()
	}
	if path := databasePath(ctx, s.db); path != "" {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() >= s.maxDBSize {
			values = append(values, candidate{"database_capacity", "warning", "database", "primary", "database volume exceeds threshold; shorten traffic retention or purge history"})
		}
	}
	if aggregationErr != nil {
		values = append(values, candidate{"aggregation_failed", "critical", "control_plane", "primary", "traffic aggregation failed"})
	}
	if trafficStalled {
		values = append(values, candidate{"traffic_stalled", "warning", "control_plane", "traffic", "traffic collection loop has not completed a successful cycle within the threshold while active proxy connections exist (V2Ray stats query or ingest keeps failing)"})
	}
	// VPS 月流量预算（提供商账单口径）：用量越线告警 + 数据源持续失效告警，
	// 由 solusvm 轮询循环注入快照驱动。
	if s.bandwidth != nil {
		if status, ok := s.bandwidth(); ok {
			if status.TotalBytes > 0 {
				percent := float64(status.UsedBytes) / float64(status.TotalBytes) * 100
				summary := fmt.Sprintf("VPS 月流量（提供商账单口径）已用 %.1f%%（%.1f GiB / %.0f GiB）", percent, float64(status.UsedBytes)/(1<<30), float64(status.TotalBytes)/(1<<30))
				switch {
				case percent >= bandwidthCriticalPercent:
					values = append(values, candidate{"vps_bandwidth_high", "critical", "control_plane", "bandwidth", summary + "，即将超额"})
				case percent >= bandwidthWarnPercent:
					values = append(values, candidate{"vps_bandwidth_high", "warning", "control_plane", "bandwidth", summary + "，接近月度配额"})
				}
			}
			if status.Failed && now.Sub(status.LastSuccess) > bandwidthStaleAfter {
				values = append(values, candidate{"vps_bandwidth_stalled", "warning", "control_plane", "bandwidth", fmt.Sprintf("VPS 流量计量已超过 %s 无成功采样（SolusVM API 持续拉取失败），月流量预算失去数据源", formatDuration(bandwidthStaleAfter))})
			}
		}
	}
	// 面板新版本：后台周期检查（或页面检查）缓存的结果里有可升级版本时提醒，
	// 升级完成（或源停配）后下一轮自动恢复。
	if s.upgrade != nil {
		if status, ok := s.upgrade(); ok && status.Available {
			values = append(values, candidate{"upgrade_available", "warning", "system_upgrade", "panel",
				fmt.Sprintf("面板有新版本 %s（当前 %s），可在「系统升级」页查看说明并升级", status.LatestVersion, status.CurrentVersion)})
		}
	}
	// 协议端到端拨测失败行驱动告警（行翻绿后下一轮自动恢复）；skipped 行
	// （停用/无凭据）不告警。直查 probe_results 与上面直查 runtime_state 同例。
	probeRows, err := s.db.QueryContext(ctx, `SELECT r.protocol_id, COALESCE(p.name, r.protocol_id), r.error FROM probe_results r LEFT JOIN protocols p ON p.id=r.protocol_id WHERE r.status='failed'`)
	if err != nil {
		return err
	}
	for probeRows.Next() {
		var protocolID, name, probeError string
		if err := probeRows.Scan(&protocolID, &name, &probeError); err != nil {
			probeRows.Close()
			return err
		}
		values = append(values, candidate{"protocol_probe_failed", "critical", "protocol", protocolID, "协议「" + name + "」端到端拨测失败：" + probeError})
	}
	if err := probeRows.Err(); err != nil {
		probeRows.Close()
		return err
	}
	probeRows.Close()
	warningUntil := now.Add(s.expiryWarning())
	certRows, err := s.db.QueryContext(ctx, `SELECT id, name, domain, not_after FROM certificates WHERE not_after > ? AND not_after <= ?`, now.Unix(), warningUntil.Unix())
	if err != nil {
		return err
	}
	for certRows.Next() {
		var id, name, domain string
		var notAfter int64
		if err := certRows.Scan(&id, &name, &domain, &notAfter); err != nil {
			certRows.Close()
			return err
		}
		remaining := time.Until(time.Unix(notAfter, 0))
		if s.now != nil {
			remaining = time.Unix(notAfter, 0).Sub(now)
		}
		label := name
		if domain != "" {
			label += "（" + domain + "）"
		}
		values = append(values, candidate{"certificate_expiring", "warning", "certificate", id, fmt.Sprintf("证书%s将于 %s 到期，剩余%s", label, time.Unix(notAfter, 0).UTC().Format("2006-01-02 15:04:05"), formatDuration(remaining))})
	}
	if err := certRows.Err(); err != nil {
		certRows.Close()
		return err
	}
	certRows.Close()
	seen := map[string]bool{}
	for _, value := range values {
		fingerprint := value.alertType + ":" + value.resourceType + ":" + value.resourceID
		seen[fingerprint] = true
		if err := s.open(ctx, fingerprint, value, now); err != nil {
			return err
		}
	}
	openRows, err := s.db.QueryContext(ctx, `SELECT fingerprint FROM operational_alerts WHERE status='open'`)
	if err != nil {
		return err
	}
	resolved := []string{}
	for openRows.Next() {
		var fingerprint string
		_ = openRows.Scan(&fingerprint)
		if !seen[fingerprint] {
			resolved = append(resolved, fingerprint)
		}
	}
	if err := openRows.Err(); err != nil {
		openRows.Close()
		return err
	}
	openRows.Close()
	for _, fingerprint := range resolved {
		if err := s.resolve(ctx, fingerprint, now); err != nil {
			return err
		}
	}
	return nil
}

func formatDuration(value time.Duration) string {
	days := int(value / (24 * time.Hour))
	if days > 0 {
		return fmt.Sprintf("%d 天", days)
	}
	hours := int(value / time.Hour)
	return fmt.Sprintf("%d 小时", hours)
}

func (s *Service) open(ctx context.Context, fingerprint string, value candidate, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	id := uuid.NewString()
	previousStatus := ""
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM operational_alerts WHERE fingerprint=?`, fingerprint).Scan(&id, &previousStatus)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operational_alerts(id,fingerprint,type,severity,resource_type,resource_id,message,status,first_seen_at,last_seen_at) VALUES(?,?,?,?,?,?,?,'open',?,?) ON CONFLICT(fingerprint) DO UPDATE SET severity=excluded.severity,message=excluded.message,status='open',last_seen_at=excluded.last_seen_at,acknowledged_at=NULL,acknowledged_by=NULL,resolved_at=NULL`, id, fingerprint, value.alertType, value.severity, value.resourceType, value.resourceID, value.message, common.UnixTime(now), common.UnixTime(now)); err != nil {
		return err
	}
	if previousStatus != "open" {
		alert, getErr := getAlertTx(ctx, tx, id)
		if getErr != nil {
			return getErr
		}
		if err = enqueueNotification(ctx, tx, alert, "alert.open", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) resolve(ctx context.Context, fingerprint string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE operational_alerts SET status='resolved',resolved_at=?,last_seen_at=? WHERE fingerprint=? AND status='open'`, common.UnixTime(now), common.UnixTime(now), fingerprint)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return tx.Commit()
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM operational_alerts WHERE fingerprint=?`, fingerprint).Scan(&id); err != nil {
		return err
	}
	alert, err := getAlertTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := enqueueNotification(ctx, tx, alert, "alert.resolved", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) List(ctx context.Context, status, severity, alertType string) ([]Alert, error) {
	query, args := `SELECT id,fingerprint,type,severity,resource_type,resource_id,message,status,first_seen_at,last_seen_at,acknowledged_at,acknowledged_by,resolved_at FROM operational_alerts WHERE 1=1`, []any{}
	for column, value := range map[string]string{"status": status, "severity": severity, "type": alertType} {
		if strings.TrimSpace(value) != "" {
			query += ` AND ` + column + `=?`
			args = append(args, strings.TrimSpace(value))
		}
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY CASE severity WHEN 'critical' THEN 0 ELSE 1 END,last_seen_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Alert{}
	for rows.Next() {
		value, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) Acknowledge(ctx context.Context, id, actorID string) (Alert, error) {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Alert{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE operational_alerts SET acknowledged_at=?,acknowledged_by=? WHERE id=? AND acknowledged_at IS NULL`, common.UnixTime(now), actorID, id)
	if err != nil {
		return Alert{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		value, getErr := getAlertTx(ctx, tx, id)
		if getErr != nil {
			return Alert{}, getErr
		}
		if err := tx.Commit(); err != nil {
			return Alert{}, err
		}
		return value, nil
	}
	value, err := getAlertTx(ctx, tx, id)
	if err != nil {
		return Alert{}, err
	}
	if err := enqueueNotification(ctx, tx, value, "alert.acknowledged", now); err != nil {
		return Alert{}, err
	}
	if err := tx.Commit(); err != nil {
		return Alert{}, err
	}
	return value, nil
}

func getAlertTx(ctx context.Context, tx *sql.Tx, id string) (Alert, error) {
	return scanAlert(tx.QueryRowContext(ctx, `SELECT id,fingerprint,type,severity,resource_type,resource_id,message,status,first_seen_at,last_seen_at,acknowledged_at,acknowledged_by,resolved_at FROM operational_alerts WHERE id=?`, id))
}

func scanAlert(row common.Scanner) (Alert, error) {
	var value Alert
	var first, last common.Time
	var acknowledgedAt, resolvedAt common.Time
	var acknowledgedBy sql.NullString
	if err := row.Scan(&value.ID, &value.Fingerprint, &value.Type, &value.Severity, &value.ResourceType, &value.ResourceID, &value.Message, &value.Status, &first, &last, &acknowledgedAt, &acknowledgedBy, &resolvedAt); err != nil {
		if err == sql.ErrNoRows {
			return Alert{}, common.ErrNotFound
		}
		return Alert{}, err
	}
	value.FirstSeenAt, value.LastSeenAt = first.Time, last.Time
	value.AcknowledgedAt = acknowledgedAt.Ptr()
	value.ResolvedAt = resolvedAt.Ptr()
	if acknowledgedBy.Valid {
		value.AcknowledgedBy = &acknowledgedBy.String
	}
	return value, nil
}

func databasePath(ctx context.Context, db *sql.DB) string {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if rows.Scan(&sequence, &name, &path) == nil && name == "main" {
			return path
		}
	}
	return ""
}
