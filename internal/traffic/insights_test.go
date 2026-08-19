package traffic

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

// newInsightsDatabase builds an in-memory database with the traffic schema and
// a minimal protocols table for the share-name subquery.
func newInsightsDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE protocols (id TEXT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("apply protocols schema: %v", err)
	}
	return db
}

// seedInsight writes one traffic_hourly row mirroring the collector's storage
// model: user rows carry user_id only, inbound rows carry protocol_id only.
func seedInsight(t *testing.T, db *sql.DB, bucket time.Time, userID, protocolID string, up, down int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO traffic_hourly(bucket_start,node_id,user_id,protocol_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,'local',?,?,?,?,1,?)`,
		common.UnixTime(bucket), userID, protocolID, up, down, common.UnixTime(bucket)); err != nil {
		t.Fatalf("seed traffic_hourly: %v", err)
	}
}

func TestActiveUsersCountsDistinctUsersPerBucket(t *testing.T) {
	db := newInsightsDatabase(t)
	hour1 := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	seedInsight(t, db, hour1, "user-a", "", 10, 20)
	seedInsight(t, db, hour1, "user-b", "", 5, 5)
	seedInsight(t, db, hour2, "user-a", "", 1, 1)
	seedInsight(t, db, hour2, "", "proto-1", 100, 100) // inbound rows are not user activity

	result, err := NewService(db).ActiveUsers(context.Background(), Filter{From: hour1, To: hour2.Add(time.Hour), Interval: "hour"})
	if err != nil {
		t.Fatalf("ActiveUsers: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].Users != 2 || result.Items[1].Users != 1 {
		t.Fatalf("items = %#v, want [2, 1] across two buckets", result.Items)
	}
	if result.TotalUsers != 2 {
		t.Fatalf("totalUsers = %d, want 2 (range-wide distinct)", result.TotalUsers)
	}
}

func TestActiveUsersDropsProtocolFilter(t *testing.T) {
	db := newInsightsDatabase(t)
	hour := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	seedInsight(t, db, hour, "user-a", "", 10, 20)
	// A protocol filter is inapplicable to user rows and must be ignored
	// instead of producing an empty result.
	result, err := NewService(db).ActiveUsers(context.Background(), Filter{From: hour, To: hour.Add(time.Hour), Interval: "hour", ProtocolID: "proto-1"})
	if err != nil {
		t.Fatalf("ActiveUsers: %v", err)
	}
	if result.TotalUsers != 1 {
		t.Fatalf("totalUsers = %d, want 1", result.TotalUsers)
	}
}

func TestProtocolSharesAggregatesInboundRows(t *testing.T) {
	db := newInsightsDatabase(t)
	hour1 := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	hour2 := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO protocols(id,name) VALUES('proto-1','VLESS'),('proto-2','TUIC')`); err != nil {
		t.Fatalf("seed protocols: %v", err)
	}
	seedInsight(t, db, hour1, "", "proto-1", 30, 20)
	seedInsight(t, db, hour2, "", "proto-1", 10, 40)
	seedInsight(t, db, hour2, "", "proto-2", 100, 0)
	seedInsight(t, db, hour2, "user-a", "", 5, 5) // user rows are not inbound traffic

	values, err := NewService(db).ProtocolShares(context.Background(), Filter{From: hour1, To: hour2.Add(time.Hour), Interval: "hour"})
	if err != nil {
		t.Fatalf("ProtocolShares: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("shares = %#v, want 2 protocols", values)
	}
	if values[0].ID != "proto-2" || values[0].TotalBytes != 100 || values[0].Name != "TUIC" {
		t.Fatalf("top share = %#v, want proto-2/TUIC with 100 bytes", values[0])
	}
	if values[1].ID != "proto-1" || values[1].TotalBytes != 100 || values[1].UpBytes != 40 || values[1].DownBytes != 60 {
		t.Fatalf("second share = %#v, want proto-1/VLESS with 40 up + 60 down", values[1])
	}
}

func TestAggregateRecentIncludesInProgressDay(t *testing.T) {
	db := newInsightsDatabase(t)
	today := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	yesterday := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	seedInsight(t, db, yesterday, "user-a", "", 10, 0)
	seedInsight(t, db, today, "user-a", "", 20, 0)
	service := NewService(db)
	service.now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) }
	if err := service.AggregateRecent(context.Background()); err != nil {
		t.Fatalf("AggregateRecent: %v", err)
	}
	got := map[string]int64{}
	rows, err := db.Query(`SELECT bucket_start,up_bytes FROM traffic_daily`)
	if err != nil {
		t.Fatalf("query daily: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket int64
		var up int64
		if err := rows.Scan(&bucket, &up); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[time.Unix(bucket, 0).UTC().Format("2006-01-02")] = up
	}
	if got["2026-08-13"] != 10 || got["2026-08-14"] != 20 {
		t.Fatalf("daily = %#v, want yesterday=10 and in-progress today=20", got)
	}
}

func TestProtocolSharesDropsUserFilter(t *testing.T) {
	db := newInsightsDatabase(t)
	hour := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	seedInsight(t, db, hour, "", "proto-1", 10, 10)
	values, err := NewService(db).ProtocolShares(context.Background(), Filter{From: hour, To: hour.Add(time.Hour), Interval: "hour", UserID: "user-a"})
	if err != nil {
		t.Fatalf("ProtocolShares: %v", err)
	}
	if len(values) != 1 || values[0].TotalBytes != 20 {
		t.Fatalf("shares = %#v, want proto-1 with 20 bytes despite user filter", values)
	}
}
