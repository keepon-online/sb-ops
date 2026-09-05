// Package backup 提供面板数据库的一键在线备份：经 VACUUM INTO 产出事务
// 一致、已紧凑化的独立快照文件，落盘 <data_dir>/backups/。备份文件含
// control_plane_secrets 主密钥与用户订阅 token，权限规格与主库一致。
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	common "github.com/keepon-online/sb-ops/internal/service/common"
)

// maxBackups 是保留的最近备份份数，创建成功后自动裁剪最旧的超限备份
// （常量硬编码，勿进 config.yaml）。
const maxBackups = 5

// backupDirName 是 data_dir 下 dedicated 存放备份的子目录名。
const backupDirName = "backups"

// backupNamePattern 严格限定备份文件名（sb-ops-backup-YYYYMMDD-HHMMSS.db，
// 时间戳 UTC）。读取类操作先过它，杜绝路径穿越与任意文件读取；子匹配用于
// StaleSnapshot 提取文件名内嵌的生成时间。
var backupNamePattern = regexp.MustCompile(`^sb-ops-backup-(\d{8}-\d{6})\.db$`)

// Service 持有与主库相同的 *sql.DB（SetMaxOpenConns(1) 下备份期间其它
// 查询短暂排队，个人规模库亚秒级）。dir 闭包实时返回 data_dir，服务不
// import internal/config（照 upgrade 的 Source 闭包先例，便于注入测试）。
type Service struct {
	db  *sql.DB
	dir func() string
	mu  sync.Mutex // 串行化创建：拦截同一秒内的重复备份（VACUUM INTO 目标已存在会失败）
	now func() time.Time
}

func NewService(db *sql.DB, dir func() string) *Service {
	return &Service{db: db, dir: dir, now: func() time.Time { return time.Now().UTC() }}
}

// Create 生成新快照并裁剪超限旧备份。同秒重复创建返回 ErrDuplicate。
func (s *Service) Create(ctx context.Context) (BackupInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupInfo{}, fmt.Errorf("创建备份目录失败: %w", err)
	}
	name := fmt.Sprintf("sb-ops-backup-%s.db", s.now().Format("20060102-150405"))
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		return BackupInfo{}, common.ErrDuplicate
	}
	// VACUUM INTO 对 WAL 库产出含未 checkpoint 事务的完整快照；目标已存在
	// 时 SQLite 自身也会报错，上面的 Stat 只为给出更友好的 ErrDuplicate。
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		// 中途失败（含停机 ctx 取消触发 interrupt）SQLite 不保证删除目标
		// 文件，兜底清掉半成品，防止截断快照混进备份列表。
		_ = os.Remove(target)
		return BackupInfo{}, fmt.Errorf("执行 VACUUM INTO 失败: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return BackupInfo{}, fmt.Errorf("收紧备份文件权限失败: %w", err)
	}
	info, err := statBackup(dir, name)
	if err != nil {
		return BackupInfo{}, err
	}
	s.prune(dir)
	return info, nil
}

// List 按创建时间倒序返回备份清单；目录不存在（从未备份过）返回空列表。
func (s *Service) List() ([]BackupInfo, error) {
	dir := s.backupDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取备份目录失败: %w", err)
	}
	values := make([]BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !backupNamePattern.MatchString(entry.Name()) {
			continue
		}
		info, err := statBackup(dir, entry.Name())
		if os.IsNotExist(err) {
			continue // 与并发删除竞争，跳过即可
		}
		if err != nil {
			return nil, err
		}
		values = append(values, info)
	}
	// 文件名编码了单调递增的时间戳，modtime 相同（同一真实秒）时以它定序，
	// 保证裁剪总是删掉真正最旧的一份。
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt != values[j].CreatedAt {
			return values[i].CreatedAt > values[j].CreatedAt
		}
		return values[i].Name > values[j].Name
	})
	return values, nil
}

// Delete 删除指定备份；文件名不合法或不存在一律 ErrNotFound。
func (s *Service) Delete(name string) error {
	if !backupNamePattern.MatchString(name) {
		return common.ErrNotFound
	}
	if err := os.Remove(filepath.Join(s.backupDir(), name)); err != nil {
		if os.IsNotExist(err) {
			return common.ErrNotFound
		}
		return fmt.Errorf("删除备份文件失败: %w", err)
	}
	return nil
}

// Path 返回指定备份的绝对路径供下载 handler 流式下发；校验口径同 Delete。
func (s *Service) Path(name string) (string, error) {
	if !backupNamePattern.MatchString(name) {
		return "", common.ErrNotFound
	}
	path := filepath.Join(s.backupDir(), name)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", common.ErrNotFound
		}
		return "", fmt.Errorf("检查备份文件失败: %w", err)
	}
	return path, nil
}

func (s *Service) backupDir() string {
	return filepath.Join(s.dir(), backupDirName)
}

// StaleSnapshot 返回磁盘上最新一份快照的生成时间（取文件名内嵌时间戳，比
// modtime 权威且可测试），以及它是否已超过 SnapshotStaleAfter 未更新。
// 目录不存在、读取失败或没有合法命名的快照时 ok=false——告警评估据此
// 区分「全新部署尚未产生快照」与「备份链断裂」，前者不评估。
func (s *Service) StaleSnapshot() (newest time.Time, stale bool) {
	entries, err := os.ReadDir(s.backupDir())
	if err != nil {
		return time.Time{}, false
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		m := backupNamePattern.FindStringSubmatch(entry.Name())
		if m == nil {
			continue
		}
		ts, err := time.ParseInLocation("20060102-150405", m[1], time.UTC)
		if err != nil {
			continue // \d{8} 允许 20261305 这类非法日期，跳过防御
		}
		if !found || ts.After(newest) {
			newest, found = ts, true
		}
	}
	if !found || s.now().Sub(newest) <= SnapshotStaleAfter {
		return newest, false
	}
	return newest, true
}

// prune 裁掉超限的最旧备份；best-effort，失败静默（手动删除兜底），不
// 让清理问题拖垮一次成功的备份。
func (s *Service) prune(dir string) {
	values, err := s.List()
	if err != nil {
		return
	}
	if len(values) <= maxBackups {
		return
	}
	for _, old := range values[maxBackups:] {
		_ = os.Remove(filepath.Join(dir, old.Name))
	}
}

func statBackup(dir, name string) (BackupInfo, error) {
	fileInfo, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return BackupInfo{}, common.ErrNotFound
		}
		return BackupInfo{}, fmt.Errorf("读取备份文件信息失败: %w", err)
	}
	return BackupInfo{Name: name, SizeBytes: fileInfo.Size(), CreatedAt: fileInfo.ModTime().Unix()}, nil
}
