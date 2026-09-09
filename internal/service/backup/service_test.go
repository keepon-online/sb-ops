package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	common "github.com/keepon-online/sb-ops/internal/service/common"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE probe (id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO probe (value) VALUES ('hello')`); err != nil {
		t.Fatalf("写入测试数据失败: %v", err)
	}
	dir := t.TempDir()
	return NewService(db, func() string { return dir }, func() string { return filepath.Join(dir, "singbox-web.db") }, nil), dir
}

func TestCreateAndList(t *testing.T) {
	service, dir := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC) }

	info, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("创建备份失败: %v", err)
	}
	if info.Name != "sb-ops-backup-20260905-103000.db" {
		t.Fatalf("备份文件名不符合命名约定: %s", info.Name)
	}
	if info.SizeBytes <= 0 {
		t.Fatalf("备份大小应大于 0: %d", info.SizeBytes)
	}
	path := filepath.Join(dir, "backups", info.Name)
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("备份文件未落盘: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("备份文件权限应为 0600，实际 %v", fileInfo.Mode().Perm())
	}

	// 备份必须是能独立打开的完整 SQLite 快照，内容与主库一致。
	snapshot, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("打开备份快照失败: %v", err)
	}
	defer snapshot.Close()
	var value string
	if err := snapshot.QueryRow(`SELECT value FROM probe LIMIT 1`).Scan(&value); err != nil || value != "hello" {
		t.Fatalf("快照内容校验失败: value=%q err=%v", value, err)
	}

	values, err := service.List()
	if err != nil {
		t.Fatalf("列出备份失败: %v", err)
	}
	if len(values) != 1 || values[0].Name != info.Name || values[0].SizeBytes != info.SizeBytes {
		t.Fatalf("列表应恰好包含刚创建的备份: %+v", values)
	}
}

func TestCreateDuplicateWithinSameSecond(t *testing.T) {
	service, _ := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC) }

	if _, err := service.Create(context.Background()); err != nil {
		t.Fatalf("首次备份失败: %v", err)
	}
	if _, err := service.Create(context.Background()); !errors.Is(err, common.ErrDuplicate) {
		t.Fatalf("同一秒内重复备份应返回 ErrDuplicate，实际: %v", err)
	}
}

func TestCreatePrunesOldest(t *testing.T) {
	service, dir := newTestService(t)
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	service.now = func() time.Time { now = now.Add(time.Second); return now }

	first := "sb-ops-backup-20260905-000001.db"
	for i := 0; i < maxBackups+1; i++ {
		if _, err := service.Create(context.Background()); err != nil {
			t.Fatalf("第 %d 份备份失败: %v", i+1, err)
		}
	}
	values, err := service.List()
	if err != nil {
		t.Fatalf("列出备份失败: %v", err)
	}
	if len(values) != maxBackups {
		t.Fatalf("应保留 %d 份备份，实际 %d", maxBackups, len(values))
	}
	if _, err := os.Stat(filepath.Join(dir, "backups", first)); !os.IsNotExist(err) {
		t.Fatalf("最旧的备份应被裁剪: %v", err)
	}
	found := false
	for _, value := range values {
		if value.Name == first {
			found = true
		}
	}
	if found {
		t.Fatalf("被裁剪的备份不应出现在列表里: %s", first)
	}
}

func TestDeleteAndPathRejectInvalidNames(t *testing.T) {
	service, _ := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC) }

	created, err := service.Create(context.Background())
	if err != nil {
		t.Fatalf("创建备份失败: %v", err)
	}
	path, err := service.Path(created.Name)
	if err != nil {
		t.Fatalf("合法文件名应返回路径: %v", err)
	}
	if path == "" {
		t.Fatal("路径不应为空")
	}

	if err := service.Delete(created.Name); err != nil {
		t.Fatalf("删除备份失败: %v", err)
	}
	if err := service.Delete(created.Name); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("删除不存在的备份应返回 ErrNotFound，实际: %v", err)
	}

	for _, name := range []string{"../../etc/passwd", "singbox-web.db", "sb-ops-backup-20260905-103000.sqlite", ""} {
		if _, err := service.Path(name); !errors.Is(err, common.ErrNotFound) {
			t.Fatalf("非法文件名 %q 应被拒绝，实际: %v", name, err)
		}
		if err := service.Delete(name); !errors.Is(err, common.ErrNotFound) {
			t.Fatalf("非法文件名 %q 删除应被拒绝，实际: %v", name, err)
		}
	}
}

func TestListWithoutAnyBackup(t *testing.T) {
	service, _ := newTestService(t)
	values, err := service.List()
	if err != nil {
		t.Fatalf("从未备份过时列表应返回空: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("应为空列表，实际 %+v", values)
	}
}

func TestNextRunUsesLocalFourAMAndSkipsCurrentMinute(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before", time.Date(2026, 9, 5, 3, 59, 0, 0, loc), time.Date(2026, 9, 5, 4, 0, 0, 0, loc)},
		{"at", time.Date(2026, 9, 5, 4, 0, 0, 0, loc), time.Date(2026, 9, 6, 4, 0, 0, 0, loc)},
		{"after", time.Date(2026, 9, 5, 12, 0, 0, 0, loc), time.Date(2026, 9, 6, 4, 0, 0, 0, loc)},
		{"month boundary", time.Date(2026, 9, 30, 23, 0, 0, 0, loc), time.Date(2026, 10, 1, 4, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextRun(tc.now); !got.Equal(tc.want) {
				t.Fatalf("NextRun=%s want %s", got, tc.want)
			}
			if got := NextRun(tc.now).Location(); got != loc {
				t.Fatalf("location=%v want %v", got, loc)
			}
		})
	}
}

func TestScheduleStateTracksLastResult(t *testing.T) {
	state := NewScheduleState()
	if _, _, attempted := state.Snapshot(); attempted {
		t.Fatal("fresh state should not be attempted")
	}
	state.RecordFailure(errors.New("disk full"))
	_, lastError, attempted := state.Snapshot()
	if !attempted || lastError != "disk full" {
		t.Fatalf("failure snapshot=%q attempted=%v", lastError, attempted)
	}
	now := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	state.RecordSuccess(now)
	lastSuccess, lastError, attempted := state.Snapshot()
	if !attempted || lastError != "" || !lastSuccess.Equal(now) {
		t.Fatalf("success snapshot=%s %q %v", lastSuccess, lastError, attempted)
	}
}

func TestStaleSnapshot(t *testing.T) {
	service, _ := newTestService(t)
	base := time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC)
	clock := base
	service.now = func() time.Time { clock = clock.Add(time.Second); return clock }

	// 无快照：不评估（区分全新部署与备份链断裂）。
	if _, stale := service.StaleSnapshot(); stale {
		t.Fatal("目录为空时不应判定 stale")
	}

	// 造两份快照，最新一份的文件名时间戳是权威判定依据。
	if _, err := service.Create(context.Background()); err != nil {
		t.Fatalf("创建备份失败: %v", err)
	}
	if _, err := service.Create(context.Background()); err != nil {
		t.Fatalf("创建备份失败: %v", err)
	}
	newest := clock // now 闭包先推进再返回，最后一次 Create 的文件名时间即 clock 当前值

	// 快照尚新（< 25h）不判 stale。
	service.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, stale := service.StaleSnapshot(); stale {
		t.Fatal("快照 2 小时前生成时不应判定 stale")
	}

	// 超过 SnapshotStaleAfter（25h）判 stale，并返回文件名内嵌的最新时间。
	service.now = func() time.Time { return base.Add(26 * time.Hour) }
	got, stale := service.StaleSnapshot()
	if !stale {
		t.Fatal("快照 26 小时未更新应判定 stale")
	}
	if !got.Equal(newest) {
		t.Fatalf("最新快照时间应为文件名时间戳 %v，实际 %v", newest, got)
	}
}

func TestCreateFailureLeavesNoTarget(t *testing.T) {
	service, dir := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 9, 5, 10, 30, 0, 0, time.UTC) }
	// 关闭底层库制造 VACUUM INTO 执行失败（ErrDuplicate 之外的错误路径）。
	if err := service.db.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	_, err := service.Create(context.Background())
	if err == nil {
		t.Fatal("库已关闭时创建备份应失败")
	}

	entries, readErr := os.ReadDir(filepath.Join(dir, "backups"))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("读取备份目录失败: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("失败的备份不应遗留目标文件，实际: %v", entries)
	}
}
