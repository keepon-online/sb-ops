package common

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func TestTimeValueWritesUnixSeconds(t *testing.T) {
	if value, err := (Time{}).Value(); err != nil || value != nil {
		t.Fatalf("零值应写入 NULL，得到 (%v, %v)", value, err)
	}
	stamp := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	value, err := (Time{stamp}).Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if value != stamp.Unix() {
		t.Fatalf("应写入 unix 秒：%v", value)
	}
}

func TestTimeScanAcceptsUnixSeconds(t *testing.T) {
	stamp := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	var value Time
	if err := value.Scan(stamp.Unix()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !value.Equal(stamp) {
		t.Fatalf("得到 %v，期望 %v", value.Time, stamp)
	}
	var zero Time
	if err := zero.Scan(nil); err != nil || !zero.IsZero() {
		t.Fatalf("Scan(nil) 应为零值，得到 (%v, %v)", zero.Time, err)
	}
	for _, src := range []any{"1755340800", 42.0, []byte{1}} {
		var bad Time
		if err := bad.Scan(src); err == nil {
			t.Fatalf("Scan(%v) 应当报错", src)
		}
	}
}

func TestTimePtrRoundTrip(t *testing.T) {
	if got := (Time{}).Ptr(); got != nil {
		t.Fatalf("零值 Ptr 应为 nil，得到 %v", got)
	}
	stamp := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	if got := (Time{stamp}).Ptr(); got == nil || !got.Equal(stamp) {
		t.Fatalf("Ptr: 得到 %v", got)
	}
	if got := TimeFromPtr(nil); !got.IsZero() {
		t.Fatalf("nil 适配应为零值，得到 %v", got)
	}
	if got := TimeFromPtr(&stamp); !got.Equal(stamp) {
		t.Fatalf("TimeFromPtr: 得到 %v", got)
	}
}

// 通过真实的 sqlite 读写回环验证 Valuer/Scanner 与驱动协作无误。
func TestTimeSQLiteRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE stamps (id INTEGER PRIMARY KEY, at INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	stamp := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id    int
		value Time
	}{
		{1, Time{stamp}},
		{2, Time{}},
	} {
		if _, err := db.Exec(`INSERT INTO stamps (id, at) VALUES (?, ?)`, item.id, item.value); err != nil {
			t.Fatalf("insert %d: %v", item.id, err)
		}
	}
	rows, err := db.Query(`SELECT at FROM stamps WHERE id IN (1, 2) ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var read []Time
	for rows.Next() {
		var value Time
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan: %v", err)
		}
		read = append(read, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(read) != 2 || !read[0].Equal(stamp) || !read[1].IsZero() {
		t.Fatalf("回环结果不符：%v", read)
	}
	var isNull int
	if err := db.QueryRow(`SELECT COUNT(1) FROM stamps WHERE at IS NULL`).Scan(&isNull); err != nil || isNull != 1 {
		t.Fatalf("零值应落 NULL（count=%d, err=%v）", isNull, err)
	}
}
