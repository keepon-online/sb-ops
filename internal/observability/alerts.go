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

type Service struct {
	db            *sql.DB
	notifier      Notifier
	now           func() time.Time
	expiryWarning func() time.Duration
	maxDBSize     int64
}

func NewService(db *sql.DB, notifier Notifier) *Service {
	return &Service{db: db, notifier: notifier, now: func() time.Time { return time.Now().UTC() }, expiryWarning: func() time.Duration { return 30 * 24 * time.Hour }, maxDBSize: 10 << 30}
}

// SetExpiryWarning injects the live runtime configuration without coupling the
// observability package to the config package.
func (s *Service) SetExpiryWarning(value func() time.Duration) {
	s.expiryWarning = value
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
