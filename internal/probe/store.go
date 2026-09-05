package probe

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Item 是单个协议的一次拨测结论。status: ok | failed | skipped。
type Item struct {
	ProtocolID   string    `json:"protocolId"`
	ProtocolName string    `json:"protocolName"`
	Status       string    `json:"status"`
	LatencyMS    *int64    `json:"latencyMs"`
	Error        string    `json:"error,omitempty"`
	CheckedAt    time.Time `json:"checkedAt"`
}

// Result 是一轮拨测的完整结论。
type Result struct {
	CheckedAt time.Time `json:"checkedAt"`
	Items     []Item    `json:"items"`
}

// StoredResult 是读取面响应：各协议最近一次落库结果（无轮次概念）。
type StoredResult struct {
	Items []Item `json:"items"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Upsert 逐协议覆盖最近一次拨测结果；空列表为无操作。
func (s *Store) Upsert(ctx context.Context, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range items {
		var latency any
		if item.LatencyMS != nil {
			latency = *item.LatencyMS
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO probe_results(protocol_id,status,latency_ms,error,checked_at) VALUES(?,?,?,?,?)
ON CONFLICT(protocol_id) DO UPDATE SET status=excluded.status,latency_ms=excluded.latency_ms,error=excluded.error,checked_at=excluded.checked_at`,
			item.ProtocolID, item.Status, latency, item.Error, item.CheckedAt.Unix()); err != nil {
			return fmt.Errorf("写入拨测结果 %s: %w", item.ProtocolID, err)
		}
	}
	return tx.Commit()
}

// List 返回全部已落库的拨测结果（checked_at 升序，稳定展示）。名字读取时
// 经 JOIN 解析（拨测行只存 protocol_id）——协议改名自动跟随；协议已删而
// 残留行未级联时回退显示 protocol_id。
func (s *Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.protocol_id,COALESCE(p.name,r.protocol_id),r.status,r.latency_ms,r.error,r.checked_at FROM probe_results r LEFT JOIN protocols p ON p.id=r.protocol_id ORDER BY r.checked_at,r.protocol_id`)
	if err != nil {
		return nil, fmt.Errorf("读取拨测结果失败: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var item Item
		var latency sql.NullInt64
		var checkedAt int64
		if err := rows.Scan(&item.ProtocolID, &item.ProtocolName, &item.Status, &latency, &item.Error, &checkedAt); err != nil {
			return nil, err
		}
		if latency.Valid {
			value := latency.Int64
			item.LatencyMS = &value
		}
		item.CheckedAt = time.Unix(checkedAt, 0).UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}
