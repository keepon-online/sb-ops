package traffic

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

func TestCollectionStalenessTracksRecordCollection(t *testing.T) {
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	service := NewService(nil)
	service.now = func() time.Time { return now }

	if !service.CollectionStalled(now.Add(6*time.Minute), 5*time.Minute) {
		t.Fatal("expected stalled before the first successful collection")
	}

	service.RecordCollection()
	if service.LastCollectedAt() != now {
		t.Fatalf("lastCollectedAt=%v, want %v", service.LastCollectedAt(), now)
	}
	if service.CollectionStalled(now.Add(time.Minute), 5*time.Minute) {
		t.Fatal("expected healthy within the threshold")
	}
	if !service.CollectionStalled(now.Add(10*time.Minute), 5*time.Minute) {
		t.Fatal("expected stalled after the threshold")
	}
}

func TestPurgeBeforeAgesOutOldTraffic(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	oldTime := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	for _, table := range []string{"traffic_hourly", "traffic_daily"} {
		for _, bucket := range []time.Time{oldTime, newTime} {
			if _, err := db.Exec(`INSERT INTO `+table+`(bucket_start,node_id,user_id,protocol_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,'local','u','p',10,20,1,?)`, common.UnixTime(bucket), common.UnixTime(bucket)); err != nil {
				t.Fatalf("seed %s: %v", table, err)
			}
		}
	}
	for index, collected := range []time.Time{oldTime, newTime} {
		if _, err := db.Exec(`INSERT INTO traffic_batches(id,node_id,boot_id,sequence,payload_hash,collected_at,received_at) VALUES(?,?,?,?,?,?,?)`, "batch-"+collected.Format("20060102"), "local", "boot", index+1, "hash", common.UnixTime(collected), common.UnixTime(collected)); err != nil {
			t.Fatalf("seed batches: %v", err)
		}
	}

	service := NewService(db)
	removed, err := service.PurgeBefore(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PurgeBefore: %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed=%d, want 3 (one per table)", removed)
	}

	for _, table := range []string{"traffic_hourly", "traffic_daily"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM `+table+` WHERE bucket_start<?`, common.UnixTime(cutoff)).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d old rows", table, count)
		}
	}
	var batchCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM traffic_batches WHERE collected_at<?`, common.UnixTime(cutoff)).Scan(&batchCount); err != nil {
		t.Fatalf("count batches: %v", err)
	}
	if batchCount != 0 {
		t.Fatalf("traffic_batches retained %d old rows", batchCount)
	}
	var kept int
	if err := db.QueryRow(`SELECT COUNT(1) FROM traffic_batches WHERE collected_at>=?`, common.UnixTime(cutoff)).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("kept batches=%d err=%v", kept, err)
	}
}
