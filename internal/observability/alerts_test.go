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
