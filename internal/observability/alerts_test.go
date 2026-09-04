package observability

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/service"
	_ "github.com/mattn/go-sqlite3"
)

func TestEvaluateOpensAndResolvesTrafficStalledAlert(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{resource.Schema, Schema, ProbeSchema} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	}
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	service := NewService(db, nil)
	service.now = func() time.Time { return now }

	if err := service.Evaluate(context.Background(), nil, true); err != nil {
		t.Fatalf("Evaluate (stalled): %v", err)
	}
	alerts, err := service.List(context.Background(), "open", "", "traffic_stalled")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Severity != "warning" {
		t.Fatalf("open alerts=%+v", alerts)
	}

	// A healthy collection cycle resolves the alert.
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (healthy): %v", err)
	}
	open, err := service.List(context.Background(), "open", "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open alerts after resolve=%+v", open)
	}
	resolved, err := service.List(context.Background(), "resolved", "", "traffic_stalled")
	if err != nil {
		t.Fatalf("List resolved: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("resolved alerts=%+v", resolved)
	}
}

// TestEvaluateOpensAndResolvesProtocolProbeAlert 拨测失败行开 critical 告警
// （message 带协议名与错误），行翻绿后下一轮评估自动恢复。
func TestEvaluateOpensAndResolvesProtocolProbeAlert(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{resource.Schema, Schema, ProbeSchema} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO protocols(id,name,type,listen,listen_port,created_at,updated_at) VALUES('p1','主线路','shadowsocks','::',8443,?,?)`, now.Unix(), now.Unix()); err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO probe_results(protocol_id,status,latency_ms,error,checked_at) VALUES('p1','failed',NULL,'握手失败',?)`, now.Unix()); err != nil {
		t.Fatalf("seed failed probe: %v", err)
	}
	// skipped 行（无凭据/停用）不告警。
	if _, err := db.Exec(`INSERT INTO probe_results(protocol_id,status,latency_ms,error,checked_at) VALUES('p2','skipped',NULL,'没有可用的分配用户与设备，无凭据可测',?)`, now.Unix()); err != nil {
		t.Fatalf("seed skipped probe: %v", err)
	}

	service := NewService(db, nil)
	service.now = func() time.Time { return now }
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	open, err := service.List(context.Background(), "open", "", "protocol_probe_failed")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 1 || open[0].Severity != "critical" || open[0].ResourceID != "p1" {
		t.Fatalf("open alerts=%+v", open)
	}
	if !strings.Contains(open[0].Message, "主线路") || !strings.Contains(open[0].Message, "握手失败") {
		t.Fatalf("message=%q, want 含协议名与错误", open[0].Message)
	}

	// 拨测翻绿 → 下一轮评估自动恢复。
	if _, err := db.Exec(`UPDATE probe_results SET status='ok',latency_ms=120,error='' WHERE protocol_id='p1'`); err != nil {
		t.Fatalf("mark probe ok: %v", err)
	}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate after recovery: %v", err)
	}
	open, err = service.List(context.Background(), "open", "", "protocol_probe_failed")
	if err != nil {
		t.Fatalf("List after recovery: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open alerts after recovery=%+v", open)
	}
}

func TestEvaluateOpensAndResolvesCertificateExpiryAlert(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(resource.Schema); err != nil {
		t.Fatalf("apply resource schema: %v", err)
	}
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply alert schema: %v", err)
	}
	if _, err := db.Exec(ProbeSchema); err != nil {
		t.Fatalf("apply probe schema: %v", err)
	}
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	near := now.Add(7 * 24 * time.Hour).Unix()
	far := now.Add(60 * 24 * time.Hour).Unix()
	old := now.Add(-time.Hour).Unix()
	for _, value := range []struct {
		id, name, domain string
		notAfter         int64
	}{
		{"cert-near", "临近证书", "panel.example.com", near},
		{"cert-far", "长期证书", "far.example.com", far},
		{"cert-old", "过期证书", "old.example.com", old},
	} {
		if _, err := db.Exec(`INSERT INTO certificates(id,name,domain,certificate_pem,private_key_ciphertext,fingerprint,not_before,not_after,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, value.id, value.name, value.domain, "pem", []byte("key"), "fingerprint", now.Add(-365*24*time.Hour).Unix(), value.notAfter, now.Unix(), now.Unix()); err != nil {
			t.Fatalf("seed certificate %s: %v", value.id, err)
		}
	}
	alerts := NewService(db, nil)
	alerts.now = func() time.Time { return now }
	alerts.expiryWarning = func() time.Duration { return 30 * 24 * time.Hour }
	if err := alerts.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	open, err := alerts.List(context.Background(), "open", "", "certificate_expiring")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(open) != 1 || open[0].ResourceID != "cert-near" || open[0].Severity != "warning" {
		t.Fatalf("open alerts=%+v", open)
	}
	if !strings.Contains(open[0].Message, "临近证书") || !strings.Contains(open[0].Message, "panel.example.com") || !strings.Contains(open[0].Message, "7 天") {
		t.Fatalf("message=%q", open[0].Message)
	}
	fingerprint := open[0].Fingerprint
	if err := alerts.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("repeat Evaluate: %v", err)
	}
	open, err = alerts.List(context.Background(), "open", "", "certificate_expiring")
	if err != nil || len(open) != 1 || open[0].Fingerprint != fingerprint {
		t.Fatalf("repeat open=%+v err=%v", open, err)
	}
	if _, err := db.Exec(`UPDATE certificates SET not_after=? WHERE id='cert-near'`, far); err != nil {
		t.Fatalf("move certificate expiry: %v", err)
	}
	if err := alerts.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("recovery Evaluate: %v", err)
	}
	resolved, err := alerts.List(context.Background(), "resolved", "", "certificate_expiring")
	if err != nil || len(resolved) != 1 || resolved[0].Fingerprint != fingerprint {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}

func TestHealthStateTracksTrafficCollection(t *testing.T) {
	state := NewHealthState(nil)
	state.now = func() time.Time { return time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC) }
	state.MarkBackgroundRunning(true)

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	state.RecordTrafficCollection(nil)
	snapshot := state.Snapshot(context.Background(), db, 1)
	if snapshot.Checks["trafficCollection"].Status != "ok" {
		t.Fatalf("trafficCollection=%+v", snapshot.Checks["trafficCollection"])
	}

	state.now = func() time.Time { return time.Date(2026, 8, 13, 10, 10, 0, 0, time.UTC) }
	state.RecordTrafficCollection(errors.New("traffic collection stalled"))
	snapshot = state.Snapshot(context.Background(), db, 1)
	if snapshot.Checks["trafficCollection"].Status != "failed" {
		t.Fatalf("trafficCollection after failure=%+v", snapshot.Checks["trafficCollection"])
	}
}

func newAlertTestService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{resource.Schema, Schema, ProbeSchema} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	}
	return NewService(db, nil)
}

func openAlerts(t *testing.T, service *Service, alertType string) []Alert {
	t.Helper()
	alerts, err := service.List(context.Background(), "open", "", alertType)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return alerts
}

func TestEvaluateVPSBandwidthHighEscalatesAndResolves(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	service := newAlertTestService(t)
	service.now = func() time.Time { return now }
	var status BandwidthStatus
	var configured bool
	service.SetBandwidthSource(func() (BandwidthStatus, bool) { return status, configured })

	// 未配置数据源：任何状态都不产生候选。
	configured = false
	status = BandwidthStatus{Failed: true}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (unconfigured): %v", err)
	}
	if len(openAlerts(t, service, "")) != 0 {
		t.Fatal("未配置数据源不应产生任何告警")
	}

	configured = true
	cases := []struct {
		name       string
		used, tota int64
		severity   string
	}{
		{"79% 不告警", 79, 100, ""},
		{"80% warning", 80, 100, "warning"},
		{"95% critical", 96, 100, "critical"},
		{"回落自动恢复", 10, 100, ""},
	}
	for _, testCase := range cases {
		status = BandwidthStatus{UsedBytes: testCase.used, TotalBytes: testCase.tota}
		if err := service.Evaluate(context.Background(), nil, false); err != nil {
			t.Fatalf("Evaluate (%s): %v", testCase.name, err)
		}
		alerts := openAlerts(t, service, "vps_bandwidth_high")
		if testCase.severity == "" {
			if len(alerts) != 0 {
				t.Fatalf("%s：不应有 open 告警，got %+v", testCase.name, alerts)
			}
			continue
		}
		if len(alerts) != 1 || alerts[0].Severity != testCase.severity {
			t.Fatalf("%s：alerts=%+v want 1 条 %s", testCase.name, alerts, testCase.severity)
		}
	}
}

func TestEvaluateVPSBandwidthStalledOnlyAfterSustainedFailure(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	service := newAlertTestService(t)
	service.now = func() time.Time { return now }
	var status BandwidthStatus
	service.SetBandwidthSource(func() (BandwidthStatus, bool) { return status, true })

	// 短暂失败（1 小时前仍有成功采样）不打扰。
	status = BandwidthStatus{Failed: true, LastSuccess: now.Add(-1 * time.Hour)}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (brief failure): %v", err)
	}
	if alerts := openAlerts(t, service, "vps_bandwidth_stalled"); len(alerts) != 0 {
		t.Fatalf("短暂失败不应告警：%+v", alerts)
	}

	// 持续失败超过阈值（6 小时无成功采样）告警，恢复成功后自动解除。
	status = BandwidthStatus{Failed: true, LastSuccess: now.Add(-7 * time.Hour)}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (sustained failure): %v", err)
	}
	if alerts := openAlerts(t, service, "vps_bandwidth_stalled"); len(alerts) != 1 || alerts[0].Severity != "warning" {
		t.Fatalf("alerts=%+v want 1 条 warning", alerts)
	}
	status = BandwidthStatus{LastSuccess: now}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (recovered): %v", err)
	}
	if alerts := openAlerts(t, service, "vps_bandwidth_stalled"); len(alerts) != 0 {
		t.Fatalf("恢复后应自动解除：%+v", alerts)
	}
}

func TestEvaluateUpgradeAvailableAlert(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	service := newAlertTestService(t)
	service.now = func() time.Time { return now }
	var status UpgradeStatus
	var configured bool
	service.SetUpgradeSource(func() (UpgradeStatus, bool) { return status, configured })

	// 未配置升级源或尚无检查结果：不告警。
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (unconfigured): %v", err)
	}
	if alerts := openAlerts(t, service, "upgrade_available"); len(alerts) != 0 {
		t.Fatalf("未配置不应告警：%+v", alerts)
	}

	// 有可升级版本：warning 告警。
	configured = true
	status = UpgradeStatus{CurrentVersion: "v0.3.5", LatestVersion: "v0.3.6", Available: true}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (available): %v", err)
	}
	alerts := openAlerts(t, service, "upgrade_available")
	if len(alerts) != 1 || alerts[0].Severity != "warning" || !strings.Contains(alerts[0].Message, "v0.3.6") {
		t.Fatalf("alerts=%+v", alerts)
	}

	// 升级完成（或已是最新）后下一轮自动恢复。
	status = UpgradeStatus{CurrentVersion: "v0.3.6", LatestVersion: "v0.3.6"}
	if err := service.Evaluate(context.Background(), nil, false); err != nil {
		t.Fatalf("Evaluate (upgraded): %v", err)
	}
	if alerts := openAlerts(t, service, "upgrade_available"); len(alerts) != 0 {
		t.Fatalf("升级后应自动解除：%+v", alerts)
	}
}
