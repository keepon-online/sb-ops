package common

import (
	"errors"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// Scanner is the subset of database/sql scanning shared by *sql.Row and
// *sql.Rows, letting a store reuse one row decoder for single and list queries.
type Scanner interface {
	Scan(dest ...any) error
}

// UnixTime 把时间归一化为 unix 秒，作为时间列写入与范围比较的绑定参数。
func UnixTime(value time.Time) int64 { return value.Unix() }

// MapSQLiteError translates sqlite3 constraint violations into domain errors
// using extended result codes rather than the driver's error-message text.
func MapSQLiteError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) {
		return err
	}
	switch sqliteErr.ExtendedCode {
	case sqlite3.ErrConstraintUnique, sqlite3.ErrConstraintPrimaryKey:
		return ErrDuplicate
	case sqlite3.ErrConstraintForeignKey:
		return ErrReferenced
	default:
		return err
	}
}
