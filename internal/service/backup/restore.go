package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/keepon-online/sb-ops/internal/database"
	common "github.com/keepon-online/sb-ops/internal/service/common"
)

// stagingName 是恢复暂存文件在 backups 目录下的固定名：上传与「恢复已有
// 快照」共用同一条管线（校验 → 暂存 → 触发换文件），重复恢复直接覆盖。
const stagingName = "restore-staging.db"

// preRestoreSuffix 是恢复时被换下的当前库文件后缀。单份覆盖、不自动
// 清理——照自升级 .bak 的手动回滚语义，SSH 把它 rename 回 db_path 即可
// 回到恢复前状态。
const preRestoreSuffix = ".pre-restore"

// ErrRestoreBusy 表示一次恢复已在进行（触发后进程即将重启，窗口极短）。
var ErrRestoreBusy = errors.New("已有恢复操作正在进行")

// ErrRestoreUnsupported 表示运行环境未注入恢复触发钩子（测试装配）。
var ErrRestoreUnsupported = errors.New("当前环境不支持恢复操作")

// ErrInvalidSnapshot 标记「内容不满足恢复条件」的校验失败（非 SQLite、
// 非 sb-ops 库、schema 超前等），HTTP 层据此回 400 而不是 500。
var ErrInvalidSnapshot = errors.New("备份文件校验失败")

// stagingPath 返回暂存文件绝对路径，并确保备份目录存在。
func (s *Service) stagingPath() (string, error) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	return filepath.Join(dir, stagingName), nil
}

// validateSnapshot 对暂存副本做恢复前校验：是完好的 SQLite、是 sb-ops 的
// 库、schema 版本不超前于当前二进制（旧版本备份由启动迁移幂等拉平，新
// 版本备份直接拒绝——旧代码缺迁移会把它跑坏）。打开时不带 WAL 参数，
// 校验全程只读不落盘。
func validateSnapshot(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return fmt.Errorf("打开备份文件失败: %w", err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil {
		return fmt.Errorf("备份文件不是有效的 SQLite 数据库: %w", err)
	}
	if check != "ok" {
		return fmt.Errorf("备份文件完整性校验未通过（%s）", check)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_versions`).Scan(&version); err != nil {
		return fmt.Errorf("备份不是 sb-ops 的数据库快照（缺少 schema_versions 表）: %w", err)
	}
	if version <= 0 {
		return fmt.Errorf("备份不是 sb-ops 的数据库快照（schema 版本为空）")
	}
	if version > database.CurrentSchemaVersion {
		return fmt.Errorf("备份来自更新版本的面板（schema %d > 当前 %d），请先升级面板再恢复", version, database.CurrentSchemaVersion)
	}
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name IN ('users','protocols') LIMIT 1`).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("备份不是 sb-ops 的数据库快照（缺少资源表）")
		}
		return fmt.Errorf("检查备份表结构失败: %w", err)
	}
	return nil
}

// PrepareUpload 把上传的备份内容落盘暂存并校验；任何失败都会清掉暂存，
// 半成品不会留在备份目录里。
func (s *Service) PrepareUpload(ctx context.Context, reader io.Reader) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := s.stagingPath()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入暂存文件失败: %w", err)
	}
	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return fmt.Errorf("保存上传的备份失败: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("保存上传的备份失败: %w", err)
	}
	if err := validateSnapshot(ctx, target); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("%w: %s", ErrInvalidSnapshot, err)
	}
	return nil
}

// StageExisting 把备份列表中的一份快照复制到暂存并校验（复制而非移动：
// 列表原件保留，恢复失败/放弃时快照仍在）。
func (s *Service) StageExisting(ctx context.Context, name string) error {
	if !backupNamePattern.MatchString(name) {
		return common.ErrNotFound
	}
	source, err := s.Path(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	target, err := s.stagingPath()
	if err != nil {
		return err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("读取备份文件失败: %w", err)
	}
	defer sourceFile.Close()
	targetFile, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入暂存文件失败: %w", err)
	}
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		_ = targetFile.Close()
		_ = os.Remove(target)
		return fmt.Errorf("复制备份到暂存失败: %w", err)
	}
	if err := targetFile.Close(); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("复制备份到暂存失败: %w", err)
	}
	if err := validateSnapshot(ctx, target); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("%w: %s", ErrInvalidSnapshot, err)
	}
	return nil
}

// TriggerRestore 触发恢复：把换库文件动作注册进 main 停机链（db.Close
// 之后、exec 重生前执行），随后进程走完整优雅停机并以备份库重生。返回
// nil 只代表恢复已受理，不代表已完成——响应必须赶在停机 drain 前发出。
func (s *Service) TriggerRestore() error {
	if s.restoreTrigger == nil {
		return ErrRestoreUnsupported
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 触发后进程即进入停机，字段无需复位；守卫的是停机 drain 前挤进来
	// 的并发请求——重复注册换文件动作没有任何意义。
	if s.restoring {
		return ErrRestoreBusy
	}
	target, err := s.stagingPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("暂存快照不存在（校验未通过或已被清理），请重新选择备份: %w", common.ErrNotFound)
		}
		return fmt.Errorf("检查暂存快照失败: %w", err)
	}
	current := s.dbPath()
	staging := target
	s.restoring = true
	s.restoreTrigger(func() error { return swapDatabase(current, staging) })
	return nil
}

// swapDatabase 用暂存快照替换当前库文件，由 main 在数据库 Close 之后调用：
// 旧库先 rename 保命（.pre-restore，手动回滚点），换入的快照是 VACUUM
// 产物、自身不带 WAL，但当前库的 -wal/-shm 残留必须清掉——否则新库打开
// 时 SQLite 会把旧 WAL 当作待恢复日志回放进新库。任何一步失败都把旧库
// 移回原位再返回错误，调用方据此放弃重启。
func swapDatabase(dbPath, staging string) error {
	safety := dbPath + preRestoreSuffix
	_ = os.Remove(safety) // 单份覆盖：上一次恢复留下的保命文件让位
	if err := os.Rename(dbPath, safety); err != nil {
		return fmt.Errorf("保命备份当前数据库失败: %w", err)
	}
	if err := os.Rename(staging, dbPath); err != nil {
		if rollback := os.Rename(safety, dbPath); rollback != nil {
			return fmt.Errorf("换入快照失败且回滚失败（当前库在 %s，需手动恢复）: %w", safety, errors.Join(err, rollback))
		}
		return fmt.Errorf("换入快照失败（已回滚）: %w", err)
	}
	// 旧进程已 Close：正常停机会 checkpoint 并删掉 WAL，这里兜底清理防
	// 异常停机残留；unlink 已打开的文件对 POSIX 无害（此时无进程持有）。
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("收紧恢复后库文件权限失败: %w", err)
	}
	return nil
}
