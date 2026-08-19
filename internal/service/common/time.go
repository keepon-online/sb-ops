package common

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Time 是可直接作为 scan 目标或写入参数的时间包装，免去各 store 手写的
// 时间列转换。存储值为 unix 秒（INTEGER），零值代表 SQL NULL，与模型层用
// nil *time.Time 表示缺失时间戳的约定一致；写入零值 Time 会落 NULL。
type Time struct{ time.Time }

func (t Time) Value() (driver.Value, error) {
	if t.IsZero() {
		return nil, nil
	}
	return t.Unix(), nil
}

func (t *Time) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case int64:
		t.Time = time.Unix(value, 0).UTC()
		return nil
	default:
		return fmt.Errorf("无法从 %T 解析时间列的值", src)
	}
}

// Ptr 把零值 Time 折叠为 nil，对应模型的可空 *time.Time 字段。
func (t Time) Ptr() *time.Time {
	if t.IsZero() {
		return nil
	}
	value := t.Time
	return &value
}

// TimeFromPtr 把模型的可空时间戳适配为写入参数；nil 变为零值 Time，落库 NULL。
func TimeFromPtr(value *time.Time) Time {
	if value == nil {
		return Time{}
	}
	return Time{*value}
}
