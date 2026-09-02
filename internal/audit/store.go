package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

// Store 持有审计日志表的全部读写：Append 落盘、List 查询、PurgeBefore
// 供留存清理任务调用。
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Append(ctx context.Context, entry Record) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_logs (
  id, actor_id, action, resource_type, resource_id, result,
  request_id, client_ip, details_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, optionalString(entry.ActorID), entry.Action, entry.ResourceType, entry.ResourceID,
		entry.Result, entry.RequestID, entry.ClientIP, entry.DetailsJSON, common.UnixTime(entry.CreatedAt),
	)
	return err
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

// PurgeBefore 删除 before 之前的审计记录，返回删除行数；供
// audit.retention_days 留存任务调用。
func (s *Store) PurgeBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM audit_logs WHERE created_at < ?`, common.UnixTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) List(ctx context.Context, input Query) (Page, error) {
	offset, _ := strconv.Atoi(input.Cursor)
	if offset < 0 {
		offset = 0
	}
	limit := input.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}
	where := []string{"1=1"}
	args := []any{}
	add := func(column, value string) {
		if strings.TrimSpace(value) != "" {
			where = append(where, column+"=?")
			args = append(args, strings.TrimSpace(value))
		}
	}
	add("actor_id", input.Actor)
	add("resource_type", input.Resource)
	add("action", input.Action)
	add("result", input.Result)
	if input.From != "" {
		where = append(where, "created_at>=?")
		args = append(args, input.From)
	}
	if input.To != "" {
		where = append(where, "created_at<=?")
		args = append(args, input.To)
	}
	filter := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM audit_logs WHERE `+filter, args...).Scan(&total); err != nil {
		return Page{}, err
	}
	queryArgs := append(append([]any{}, args...), limit+1, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id,actor_id,action,resource_type,resource_id,result,request_id,client_ip,details_json,created_at FROM audit_logs WHERE `+filter+` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Items: []Entry{}, Total: total}
	for rows.Next() {
		var value Entry
		var actor sql.NullString
		var details string
		var created common.Time
		if err := rows.Scan(&value.ID, &actor, &value.Action, &value.ResourceType, &value.ResourceID, &value.Result, &value.RequestID, &value.ClientIP, &details, &created); err != nil {
			return Page{}, err
		}
		if actor.Valid {
			value.ActorID = &actor.String
		}
		_ = json.Unmarshal([]byte(details), &value.Details)
		value.CreatedAt = created.Time
		page.Items = append(page.Items, value)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.NextCursor = strconv.Itoa(offset + limit)
	}
	return page, rows.Err()
}
