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

type Query struct {
	Cursor                                    string
	Limit                                     int
	Actor, Resource, Action, Result, From, To string
}
type Entry struct {
	ID           string    `json:"id"`
	ActorID      *string   `json:"actorId,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Result       string    `json:"result"`
	RequestID    string    `json:"requestId"`
	ClientIP     string    `json:"clientIp"`
	Details      any       `json:"details"`
	CreatedAt    time.Time `json:"createdAt"`
}
type Page struct {
	Items      []Entry `json:"items"`
	NextCursor string  `json:"nextCursor,omitempty"`
	Total      int     `json:"total"`
}
type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) List(ctx context.Context, input Query) (Page, error) {
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
