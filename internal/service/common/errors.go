package common

import "errors"

var (
	ErrNotFound         = errors.New("资源不存在")
	ErrRevisionConflict = errors.New("资源已被修改")
	ErrReferenced       = errors.New("资源被引用，无法删除")
	ErrDuplicate        = errors.New("资源已存在")
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return "资源校验失败"
}
