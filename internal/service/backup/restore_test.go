package backup

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/keepon-online/sb-ops/internal/database"
)

// buildSnapshot 用真实迁移链构造合法备份快照：内存库跑完整 Migrate 后
// VACUUM INTO 落盘，与生产备份的字节形态一致。
func buildSnapshot(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("迁移内存库失败: %v", err)
	}
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatalf("生成快照失败: %v", err)
	}
}

func newRestoreService(t *testing.T) (*Service, string, string, *[]func() error) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "singbox-web.db")
	if err := os.WriteFile(dbPath, []byte("current-database"), 0o600); err != nil {
		t.Fatalf("写当前库占位失败: %v", err)
	}
	var actions []func() error
	service := NewService(nil, func() string { return dir }, func() string { return dbPath }, func(action func() error) {
		actions = append(actions, action)
	})
	return service, dir, dbPath, &actions
}

func TestPrepareUploadAcceptsValidSnapshot(t *testing.T) {
	service, dir, _, _ := newRestoreService(t)
	snapshot := filepath.Join(dir, "source.db")
	buildSnapshot(t, snapshot)
	content, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	if err := service.PrepareUpload(context.Background(), bytes.NewReader(content)); err != nil {
		t.Fatalf("PrepareUpload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, backupDirName, stagingName)); err != nil {
		t.Fatalf("暂存文件应存在: %v", err)
	}
}

func TestPrepareUploadRejectsInvalidSnapshots(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{name: "非 SQLite 文本", content: []byte("this is not a database")},
		{name: "空文件", content: []byte{}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			service, dir, _, _ := newRestoreService(t)
			if err := service.PrepareUpload(context.Background(), bytes.NewReader(item.content)); err == nil {
				t.Fatalf("应拒绝非法内容")
			} else if err.Error()[:len(ErrInvalidSnapshot.Error())] != ErrInvalidSnapshot.Error() {
				t.Fatalf("错误应标记为校验失败，实际: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, backupDirName, stagingName)); !os.IsNotExist(err) {
				t.Fatalf("失败的暂存应被清理")
			}
		})
	}
}

func TestPrepareUploadRejectsFutureSchema(t *testing.T) {
	service, dir, _, _ := newRestoreService(t)
	snapshot := filepath.Join(dir, "future.db")
	buildSnapshot(t, snapshot)
	db, err := sql.Open("sqlite3", snapshot)
	if err != nil {
		t.Fatalf("打开快照失败: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_versions SET version=version+100`); err != nil {
		t.Fatalf("改写 schema 版本失败: %v", err)
	}
	_ = db.Close()
	content, _ := os.ReadFile(snapshot)
	err = service.PrepareUpload(context.Background(), bytes.NewReader(content))
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("更新版本")) {
		t.Fatalf("应拒绝超前 schema，实际: %v", err)
	}
}

func TestStageExistingCopiesAndValidates(t *testing.T) {
	service, dir, _, _ := newRestoreService(t)
	snapshot := filepath.Join(dir, backupDirName, "sb-ops-backup-20260909-040000.db")
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o700); err != nil {
		t.Fatalf("建备份目录失败: %v", err)
	}
	buildSnapshot(t, snapshot)
	if err := service.StageExisting(context.Background(), "sb-ops-backup-20260909-040000.db"); err != nil {
		t.Fatalf("StageExisting: %v", err)
	}
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("列表原件应保留: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, backupDirName, stagingName)); err != nil {
		t.Fatalf("暂存应存在: %v", err)
	}
	if err := service.StageExisting(context.Background(), "../sb-ops-backup-20260909-040000.db"); err == nil {
		t.Fatalf("非法文件名应被拒绝")
	}
}

func TestTriggerRestoreRegistersSwapAction(t *testing.T) {
	service, dir, dbPath, actions := newRestoreService(t)
	snapshot := filepath.Join(dir, backupDirName, stagingName)
	if err := os.MkdirAll(filepath.Dir(snapshot), 0o700); err != nil {
		t.Fatalf("建备份目录失败: %v", err)
	}
	if err := os.WriteFile(snapshot, []byte("staged-snapshot"), 0o600); err != nil {
		t.Fatalf("写暂存失败: %v", err)
	}
	if err := service.TriggerRestore(); err != nil {
		t.Fatalf("TriggerRestore: %v", err)
	}
	if len(*actions) != 1 {
		t.Fatalf("应注册恰好一个换文件动作，实际 %d", len(*actions))
	}
	if err := service.TriggerRestore(); err == nil || err != ErrRestoreBusy {
		t.Fatalf("二次触发应返回 ErrRestoreBusy，实际: %v", err)
	}
	// 执行注册的动作：换文件序列应完成（此处 staging 是文本占位，验证
	// rename 语义而非 SQLite 语义）。
	if err := (*actions)[0](); err != nil {
		t.Fatalf("执行换文件动作: %v", err)
	}
	current, _ := os.ReadFile(dbPath)
	if !bytes.Equal(current, []byte("staged-snapshot")) {
		t.Fatalf("当前库应被暂存内容替换")
	}
	safety, _ := os.ReadFile(dbPath + preRestoreSuffix)
	if !bytes.Equal(safety, []byte("current-database")) {
		t.Fatalf("旧库应保命为 .pre-restore")
	}
}

func TestTriggerRestoreWithoutStaging(t *testing.T) {
	service, _, _, actions := newRestoreService(t)
	if err := service.TriggerRestore(); err == nil {
		t.Fatalf("无暂存时触发应失败")
	}
	if len(*actions) != 0 {
		t.Fatalf("不应注册动作")
	}
}

func TestTriggerRestoreUnsupported(t *testing.T) {
	service := NewService(nil, func() string { return t.TempDir() }, func() string { return "unused.db" }, nil)
	if err := service.TriggerRestore(); err != ErrRestoreUnsupported {
		t.Fatalf("nil trigger 应返回 ErrRestoreUnsupported，实际: %v", err)
	}
}

func TestSwapDatabaseCleansWALSidecars(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "singbox-web.db")
	staging := filepath.Join(dir, stagingName)
	if err := os.WriteFile(dbPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("写当前库失败: %v", err)
	}
	if err := os.WriteFile(staging, []byte("new"), 0o600); err != nil {
		t.Fatalf("写暂存失败: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", []byte("stale-wal"), 0o600); err != nil {
		t.Fatalf("写 wal 残留失败: %v", err)
	}
	if err := os.WriteFile(dbPath+"-shm", []byte("stale-shm"), 0o600); err != nil {
		t.Fatalf("写 shm 残留失败: %v", err)
	}
	if err := swapDatabase(dbPath, staging); err != nil {
		t.Fatalf("swapDatabase: %v", err)
	}
	current, _ := os.ReadFile(dbPath)
	if !bytes.Equal(current, []byte("new")) {
		t.Fatalf("当前库应为暂存内容")
	}
	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("wal 残留应被清理")
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("shm 残留应被清理")
	}
	info, err := os.Stat(dbPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("恢复后库文件权限应为 0600")
	}
}

func TestSwapDatabaseRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "singbox-web.db")
	if err := os.WriteFile(dbPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("写当前库失败: %v", err)
	}
	// 暂存不存在：换入失败，旧库必须回位。
	err := swapDatabase(dbPath, filepath.Join(dir, "missing.db"))
	if err == nil {
		t.Fatalf("换入缺失暂存应失败")
	}
	current, _ := os.ReadFile(dbPath)
	if !bytes.Equal(current, []byte("old")) {
		t.Fatalf("失败后旧库应回滚到原位")
	}
}

func TestSwapDatabaseOverwritesPreviousSafetyFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "singbox-web.db")
	staging := filepath.Join(dir, stagingName)
	for _, content := range []string{"round-1", "round-2"} {
		if err := os.WriteFile(dbPath, []byte(content), 0o600); err != nil {
			t.Fatalf("写当前库失败: %v", err)
		}
		if err := os.WriteFile(staging, []byte(content+"-restored"), 0o600); err != nil {
			t.Fatalf("写暂存失败: %v", err)
		}
		if err := swapDatabase(dbPath, staging); err != nil {
			t.Fatalf("swapDatabase(%s): %v", content, err)
		}
	}
	safety, _ := os.ReadFile(dbPath + preRestoreSuffix)
	if !bytes.Equal(safety, []byte("round-2")) {
		t.Fatalf("保命文件应为最近一次恢复前的库，实际 %q", safety)
	}
}
