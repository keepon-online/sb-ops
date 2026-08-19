package observability

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/service"
	"github.com/keepon-online/sb-ops/internal/service/common"
	_ "github.com/mattn/go-sqlite3"
)

func TestPurgeNotificationsRemovesOnlyFinishedRows(t *testing.T) {
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
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	service := NewService(db, nil)
	service.now = func() time.Time { return now }
	ctx := context.Background()
	old := now.Add(-8 * 24 * time.Hour)
	stamp := common.UnixTime(old)

	insert := func(id string, delivered, failed any) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO notification_outbox(id,alert_id,event_type,payload_json,attempts,delivered_at,failed_at,available_at,created_at) VALUES(?,?,?,?,0,?,?,?,?)`,
			id, "alert-"+id, "alert.open", "{}", delivered, failed, stamp, stamp)
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("old-delivered", stamp, nil)
	insert("old-failed", nil, stamp)
	insert("recent-delivered", common.UnixTime(now), nil)
	insert("pending", nil, nil)

	removed, err := service.PurgeNotifications(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeNotifications: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d want 2", removed)
	}
	stats, err := service.NotificationStats(ctx)
	if err != nil {
		t.Fatalf("NotificationStats: %v", err)
	}
	if stats.Pending != 1 {
		t.Fatalf("pending=%d want 1", stats.Pending)
	}
	for _, id := range []string{"recent-delivered", "pending"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM notification_outbox WHERE id=?`, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("row %s count=%d err=%v want 1", id, count, err)
		}
	}
}
