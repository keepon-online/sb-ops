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

// 日界必须锚定业务时区自然日：UTC+8 下「一天」是本地 00:00~24:00，而不是
// epoch 整除出的北京 08:00~次日 08:00。
func TestAggregateRecentAnchorsDailyToLocalMidnight(t *testing.T) {
	db := newInsightsDatabase(t)
	cst := time.FixedZone("UTC+8", 8*3600)
	seedInsight(t, db, time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC), "user-a", "", 5, 0)   // 北京 8-13 23:00
	seedInsight(t, db, time.Date(2026, 8, 13, 17, 0, 0, 0, time.UTC), "user-a", "", 100, 0) // 北京 8-14 01:00
	seedInsight(t, db, time.Date(2026, 8, 14, 3, 0, 0, 0, time.UTC), "user-a", "", 20, 0)   // 北京 8-14 11:00
	service := NewService(db)
	service.now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, cst) }
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
		var bucket, up int64
		if err := rows.Scan(&bucket, &up); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[time.Unix(bucket, 0).In(cst).Format("2006-01-02")] = up
	}
	if got["2026-08-13"] != 5 || got["2026-08-14"] != 120 {
		t.Fatalf("daily = %#v, want local 8-13=5 and 8-14=120 (01:00 + 11:00)", got)
	}
}

// 存量 UTC 边界桶在启动全量重建后应改写为本地边界，且不与旧桶并存双计。
func TestRebuildDailyRewritesLegacyUTCBuckets(t *testing.T) {
	db := newInsightsDatabase(t)
	cst := time.FixedZone("UTC+8", 8*3600)
	// 北京 8-14 01:00 的一笔流量，旧版本会聚合进 UTC 8-14 00:00 桶。
	seedInsight(t, db, time.Date(2026, 8, 13, 17, 0, 0, 0, time.UTC), "user-a", "", 100, 0)
	if _, err := db.Exec(`INSERT INTO traffic_daily(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,'local','user-a','','',100,0,1,?)`,
		common.UnixTime(time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)), common.UnixTime(time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("seed legacy daily: %v", err)
	}
	service := NewService(db)
	service.now = func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, cst) }
	if err := service.RebuildDaily(context.Background()); err != nil {
		t.Fatalf("RebuildDaily: %v", err)
	}
	rows, err := db.Query(`SELECT bucket_start,up_bytes FROM traffic_daily`)
	if err != nil {
		t.Fatalf("query daily: %v", err)
	}
	defer rows.Close()
	values := [][2]int64{}
	for rows.Next() {
		var bucket, up int64
		if err := rows.Scan(&bucket, &up); err != nil {
			t.Fatalf("scan: %v", err)
		}
		values = append(values, [2]int64{bucket, up})
	}
	wantBucket := time.Date(2026, 8, 13, 16, 0, 0, 0, time.UTC).Unix() // 北京 8-14 00:00
	if len(values) != 1 || values[0][0] != wantBucket || values[0][1] != 100 {
		t.Fatalf("daily = %#v, want single local-midnight bucket %d with 100 bytes", values, wantBucket)
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

func TestRankingsDeviceDimension(t *testing.T) {
	db := newInsightsDatabase(t)
	if _, err := db.Exec(`CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT);
CREATE TABLE devices (id TEXT PRIMARY KEY, user_id TEXT, name TEXT);
INSERT INTO users(id, name) VALUES ('user-1', 'Alice'), ('user-2', 'Bob');
INSERT INTO devices(id, user_id, name) VALUES ('dev-1', 'user-1', 'iPhone 15'), ('dev-2', 'user-1', 'MacBook Pro'), ('dev-3', 'user-2', 'Windows PC');
`); err != nil {
		t.Fatalf("seed users and devices: %v", err)
	}

	hour := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	// Seed hourly traffic with user_id and device_id
	for _, s := range []struct {
		uid, did string
		up, down int64
	}{
		{"user-1", "dev-1", 100, 200},            // dev-1: 300
		{"user-1", "dev-2", 400, 500},            // dev-2: 900
		{"user-2", "dev-3", 50, 50},              // dev-3: 100
		{"user-1", "dev-deleted-0001", 700, 700}, // 已删除设备：仅剩流量事实行
	} {
		if _, err := db.Exec(`INSERT INTO traffic_hourly(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,'local',?,'',?,?,?,1,?)`,
			common.UnixTime(hour), s.uid, s.did, s.up, s.down, common.UnixTime(hour)); err != nil {
			t.Fatalf("seed hourly: %v", err)
		}
	}

	svc := NewService(db)
	filter := Filter{From: hour, To: hour.Add(time.Hour), Interval: "hour"}

	// 1. All devices ranking
	ranks, err := svc.Rankings(context.Background(), filter, "device", 10)
	if err != nil {
		t.Fatalf("Rankings(device): %v", err)
	}
	if len(ranks) != 4 {
		t.Fatalf("len(ranks) = %d, want 4", len(ranks))
	}
	if ranks[0].ID != "dev-deleted-0001" || ranks[0].TotalBytes != 1400 || ranks[0].Name != "已删除设备 · dev-dele" {
		t.Fatalf("top rank = %#v, want dev-deleted-0001 / 已删除设备 · dev-dele / 1400 bytes", ranks[0])
	}
	if ranks[1].ID != "dev-2" || ranks[1].TotalBytes != 900 || ranks[1].Name != "MacBook Pro (Alice)" {
		t.Fatalf("second rank = %#v, want dev-2 / MacBook Pro (Alice) / 900 bytes", ranks[1])
	}
	if ranks[2].ID != "dev-1" || ranks[2].TotalBytes != 300 || ranks[2].Name != "iPhone 15 (Alice)" {
		t.Fatalf("third rank = %#v, want dev-1 / iPhone 15 (Alice) / 300 bytes", ranks[2])
	}

	// 2. Filter by user-1: should return dev-2, dev-1 and the deleted device row
	userFilter := filter
	userFilter.UserID = "user-1"
	userRanks, err := svc.Rankings(context.Background(), userFilter, "device", 10)
	if err != nil {
		t.Fatalf("Rankings(device with userId): %v", err)
	}
	if len(userRanks) != 3 {
		t.Fatalf("len(userRanks) = %d, want 3", len(userRanks))
	}
	if userRanks[0].ID != "dev-deleted-0001" || userRanks[0].Name != "已删除设备 · dev-dele" {
		t.Fatalf("user rank 0 = %#v, want dev-deleted-0001 / 已删除设备 · dev-dele", userRanks[0])
	}
	if userRanks[1].ID != "dev-2" || userRanks[1].Name != "MacBook Pro" {
		t.Fatalf("user rank 1 = %#v, want dev-2 / MacBook Pro", userRanks[1])
	}

	// 3. Series filtered by device-1
	devFilter := filter
	devFilter.DeviceID = "dev-1"
	series, err := svc.Series(context.Background(), devFilter)
	if err != nil {
		t.Fatalf("Series(deviceId): %v", err)
	}
	if len(series) != 1 || series[0].TotalBytes != 300 {
		t.Fatalf("series = %#v, want 1 point with 300 bytes", series)
	}
}

func TestDeviceLifetimes(t *testing.T) {
	db := newInsightsDatabase(t)
	hour := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	insert := func(userID, deviceID string, up, down int64, at time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO traffic_hourly(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,'local',?,'',?,?,?,1,?)`,
			common.UnixTime(at), userID, deviceID, up, down, common.UnixTime(at)); err != nil {
			t.Fatalf("seed traffic_hourly: %v", err)
		}
	}
	insert("user-1", "dev-1", 100, 200, hour)
	insert("user-1", "dev-1", 300, 300, hour.Add(time.Hour)) // dev-1 合计 900
	insert("user-1", "dev-deleted", 700, 700, hour)          // 已删设备存量行也计入
	insert("user-1", "", 999, 999, hour)                     // 入站行（无 device_id）不进结果
	insert("user-2", "dev-other", 500, 500, hour)            // 其它用户不计入

	lifetimes, err := NewService(db).DeviceLifetimes(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("DeviceLifetimes: %v", err)
	}
	if len(lifetimes) != 2 {
		t.Fatalf("len(lifetimes) = %d, want 2: %#v", len(lifetimes), lifetimes)
	}
	if lifetimes["dev-1"] != 900 {
		t.Fatalf("dev-1 lifetime = %d, want 900", lifetimes["dev-1"])
	}
	if lifetimes["dev-deleted"] != 1400 {
		t.Fatalf("dev-deleted lifetime = %d, want 1400", lifetimes["dev-deleted"])
	}
}
