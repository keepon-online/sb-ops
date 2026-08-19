package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type Options struct {
	Path        string
	MMapSize    int64
	Synchronous string
}

func Open(options Options) (*sql.DB, error) {
	path := strings.TrimSpace(options.Path)
	if path == "" {
		return nil, errors.New("必须配置数据库路径")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径: %w", err)
	}
	if options.MMapSize <= 0 {
		options.MMapSize = 256 << 20
	}
	synchronous := normalizeSynchronous(options.Synchronous)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}

	dsn := buildDSN(path, synchronous)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(fmt.Sprintf("PRAGMA mmap_size=%d; PRAGMA temp_store=MEMORY;", options.MMapSize)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("配置数据库: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("设置数据库文件权限: %w", err)
	}
	return db, nil
}

// buildDSN 生成 mattn/go-sqlite3 的 DSN。刻意不用 file: URI 形式：
// Windows 盘符绝对路径（D:\x\y.db）会被 SQLite 的 URI 解析器当成
// authority 拒绝（invalid uri authority）；普通路径形式跨平台通用，
// 查询参数中的 PRAGMA 由驱动自行解析应用。
func buildDSN(path, synchronous string) string {
	return path + "?" + url.Values{
		"_busy_timeout": []string{"5000"},
		"_foreign_keys": []string{"ON"},
		"_journal_mode": []string{"WAL"},
		"_synchronous":  []string{synchronous},
	}.Encode()
}

func normalizeSynchronous(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "OFF", "FULL", "EXTRA":
		return strings.ToUpper(strings.TrimSpace(value))
	default:
		return "NORMAL"
	}
}
