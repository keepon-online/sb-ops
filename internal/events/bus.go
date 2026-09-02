package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type subscriber struct {
	permission func(string) bool
	channel    chan Event
}

type Bus struct {
	db          *sql.DB
	now         func() time.Time
	mu          sync.Mutex
	next        int
	subscribers map[int]subscriber
	closed      bool
}

func NewBus(db *sql.DB) *Bus {
	return &Bus{db: db, now: func() time.Time { return time.Now().UTC() }, subscribers: map[int]subscriber{}}
}

func (b *Bus) Publish(ctx context.Context, input Input) (Event, error) {
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return Event{}, err
	}
	now := b.now()
	query := `INSERT INTO system_events(type,scope,resource_id,permission,idempotency_key,payload_json,created_at) VALUES(?,?,?,?,?,?,?)`
	if input.IdempotencyKey != "" {
		query = `INSERT OR IGNORE INTO system_events(type,scope,resource_id,permission,idempotency_key,payload_json,created_at) VALUES(?,?,?,?,?,?,?)`
	}
	result, err := b.db.ExecContext(ctx, query, input.Type, input.Scope, input.ResourceID, input.Permission, optionalString(input.IdempotencyKey), string(payload), now.Unix())
	if err != nil {
		return Event{}, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 && input.IdempotencyKey != "" {
		return scanEvent(b.db.QueryRowContext(ctx, `SELECT id,type,scope,resource_id,permission,idempotency_key,payload_json,created_at FROM system_events WHERE idempotency_key=?`, input.IdempotencyKey))
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Event{}, err
	}
	value := Event{ID: id, Type: input.Type, Scope: input.Scope, ResourceID: input.ResourceID, Permission: input.Permission, IdempotencyKey: input.IdempotencyKey, Payload: payload, CreatedAt: now}
	b.mu.Lock()
	for key, target := range b.subscribers {
		if value.Permission != "" && !target.permission(value.Permission) {
			continue
		}
		select {
		case target.channel <- value:
		default:
			close(target.channel)
			delete(b.subscribers, key)
		}
	}
	b.mu.Unlock()
	return value, nil
}

// Retention prunes events that are both older than 24 hours and beyond the
// most recent 10000 rows. It runs on the operations loop instead of the
// publish hot path; dropped cursors resurface to clients as a reset signal.
func (b *Bus) Retention(ctx context.Context) error {
	_, err := b.db.ExecContext(ctx, `DELETE FROM system_events WHERE created_at<? AND id<(SELECT COALESCE(MAX(id),0)-10000 FROM system_events)`, b.now().Add(-24*time.Hour).Unix())
	return err
}

func (b *Bus) Since(ctx context.Context, after int64, permission func(string) bool) ([]Event, bool, error) {
	var minimum sql.NullInt64
	if err := b.db.QueryRowContext(ctx, `SELECT MIN(id) FROM system_events`).Scan(&minimum); err != nil {
		return nil, false, err
	}
	if after > 0 && minimum.Valid && after < minimum.Int64-1 {
		return []Event{}, true, nil
	}
	rows, err := b.db.QueryContext(ctx, `SELECT id,type,scope,resource_id,permission,idempotency_key,payload_json,created_at FROM system_events WHERE id>? ORDER BY id LIMIT 1000`, after)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	values := []Event{}
	for rows.Next() {
		value, err := scanEvent(rows)
		if err != nil {
			return nil, false, err
		}
		if value.Permission == "" || permission(value.Permission) {
			values = append(values, value)
		}
	}
	return values, false, rows.Err()
}

func (b *Bus) Subscribe(permission func(string) bool) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		// Post-shutdown subscribers get an already-closed channel: the SSE
		// handler's `!open` branch turns it into a reset reply and returns,
		// so a stream accepted just before listener close cannot block
		// http.Server.Shutdown either.
		channel := make(chan Event)
		close(channel)
		return channel, func() {}
	}
	id := b.next
	b.next++
	channel := make(chan Event, 128)
	b.subscribers[id] = subscriber{permission: permission, channel: channel}
	return channel, func() {
		b.mu.Lock()
		if target, ok := b.subscribers[id]; ok {
			delete(b.subscribers, id)
			close(target.channel)
		}
		b.mu.Unlock()
	}
}

// Shutdown closes every live subscription so long-lived SSE streams return
// instead of blocking http.Server.Shutdown, which does not cancel request
// contexts of active connections. Idempotent.
func (b *Bus) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for key, target := range b.subscribers {
		delete(b.subscribers, key)
		close(target.channel)
	}
}

func scanEvent(row common.Scanner) (Event, error) {
	var value Event
	var payload string
	var created common.Time
	var idempotency sql.NullString
	if err := row.Scan(&value.ID, &value.Type, &value.Scope, &value.ResourceID, &value.Permission, &idempotency, &payload, &created); err != nil {
		return Event{}, err
	}
	if idempotency.Valid {
		value.IdempotencyKey = idempotency.String
	}
	value.Payload = json.RawMessage(payload)
	value.CreatedAt = created.Time
	return value, nil
}

func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
