package events

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestRetentionPrunesOnlyStaleEvents(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	bus := NewBus(db)
	bus.now = func() time.Time { return now }
	ctx := context.Background()

	// Retention keeps the most recent 10000 rows regardless of age and only
	// deletes beyond that cap, so seed enough stale rows for the cap to bite.
	old := now.Add(-48 * time.Hour).Unix()
	statement, err := db.Prepare(`INSERT INTO system_events(type,scope,resource_id,permission,payload_json,created_at) VALUES('alert.open','alert','','','{}',?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := range 10002 {
		if _, err := statement.Exec(old); err != nil {
			t.Fatalf("seed stale event %d: %v", i, err)
		}
	}
	if _, err := bus.Publish(ctx, Input{Type: "alert.open", Scope: "alert", Payload: map[string]any{"id": "fresh"}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if err := bus.Retention(ctx); err != nil {
		t.Fatalf("Retention: %v", err)
	}
	// The fresh event (last id) and the 10000 most recent stale rows survive;
	// only the two stale rows beyond the cap are gone.
	var remaining, minID int
	if err := db.QueryRow(`SELECT COUNT(1),COALESCE(MIN(id),0) FROM system_events`).Scan(&remaining, &minID); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 10001 || minID != 3 {
		t.Fatalf("remaining=%d minID=%d want 10001 rows starting at id 3", remaining, minID)
	}
}
