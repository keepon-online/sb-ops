package observability

import (
	"context"
	"database/sql"
	"errors"
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
	for _, ddl := range []string{resource.Schema, Schema} {
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
