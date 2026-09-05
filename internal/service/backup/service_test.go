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
	return NewService(db, func() string { return dir }), dir
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
