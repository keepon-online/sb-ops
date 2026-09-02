package audit

import (
	"context"
	"database/sql"
	"time"
)

// Service 是审计日志的业务门面：handler 写入（经 h.mutate 收尾）与查询
// 端点都只面对它；SQL 细节在 Store。
type Service struct{ store *Store }

func NewService(db *sql.DB) *Service { return &Service{store: NewStore(db)} }

func (s *Service) Append(ctx context.Context, entry Record) error {
	return s.store.Append(ctx, entry)
}

func (s *Service) List(ctx context.Context, input Query) (Page, error) {
	return s.store.List(ctx, input)
}

// PurgeBefore 删除 before 之前的审计记录，返回删除行数。
func (s *Service) PurgeBefore(ctx context.Context, before time.Time) (int64, error) {
	return s.store.PurgeBefore(ctx, before)
}
