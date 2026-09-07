package traffic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

func (s *Service) AggregateRecent(ctx context.Context) error {
	now := s.now().UTC()
	start := now.Truncate(24*time.Hour).AddDate(0, 0, -3)
	// Include the in-progress day so day-granularity queries see today's traffic;
	// the window is deleted and re-inserted on every call, making the rolling
	// re-aggregation idempotent.
	end := now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM traffic_daily WHERE bucket_start>=? AND bucket_start<?`, common.UnixTime(start), common.UnixTime(end)); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO traffic_daily(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) SELECT (bucket_start/86400)*86400,node_id,user_id,protocol_id,device_id,SUM(up_bytes),SUM(down_bytes),SUM(sample_count),? FROM traffic_hourly WHERE bucket_start>=? AND bucket_start<? GROUP BY (bucket_start/86400)*86400,node_id,user_id,protocol_id,device_id`, common.UnixTime(now), common.UnixTime(start), common.UnixTime(end))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO aggregate_watermarks(name,completed_through,updated_at) VALUES('traffic_daily',?,?) ON CONFLICT(name) DO UPDATE SET completed_through=excluded.completed_through,updated_at=excluded.updated_at`, common.UnixTime(end), common.UnixTime(now))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func normalizeFilter(filter Filter) (Filter, error) {
	filter.Interval = strings.ToLower(strings.TrimSpace(filter.Interval))
	if filter.Interval == "" {
		filter.Interval = "hour"
	}
	if filter.Interval != "hour" && filter.Interval != "day" {
		return Filter{}, fmt.Errorf("%w: 时间间隔必须是 hour 或 day", ErrInvalidRequest)
	}
	if filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) {
		return Filter{}, fmt.Errorf("%w: from 和 to 必须定义有效的时间范围", ErrInvalidRequest)
	}
	return filter, nil
}

func queryWhere(filter Filter) (string, []any) {
	where := ` WHERE bucket_start>=? AND bucket_start<?`
	args := []any{common.UnixTime(filter.From), common.UnixTime(filter.To)}
	if filter.UserID != "" {
		where += ` AND user_id=?`
		args = append(args, filter.UserID)
	}
	if filter.ProtocolID != "" {
		where += ` AND protocol_id=?`
		args = append(args, filter.ProtocolID)
	}
	if filter.DeviceID != "" {
		where += ` AND device_id=?`
		args = append(args, filter.DeviceID)
	}
	return where, args
}

// totalWhere adds a guard that excludes per-inbound (protocol-only) rows from
// aggregate totals, unless the query is explicitly scoped to a protocol. Per-user
// and per-inbound stats describe the same traffic from different angles, so a
// bare total must count only one of them.
func totalWhere(filter Filter) (string, []any) {
	where, args := queryWhere(filter)
	if filter.ProtocolID == "" {
		where += ` AND user_id != ''`
	}
	return where, args
}

func (s *Service) Series(ctx context.Context, filter Filter) ([]Point, error) {
	filter, err := normalizeFilter(filter)
	if err != nil {
		return nil, err
	}
	table := "traffic_hourly"
	if filter.Interval == "day" {
		if err := s.AggregateRecent(ctx); err != nil {
			return nil, err
		}
		table = "traffic_daily"
	}
	where, args := totalWhere(filter)
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_start,SUM(up_bytes),SUM(down_bytes) FROM `+table+where+` GROUP BY bucket_start ORDER BY bucket_start`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Point{}
	for rows.Next() {
		var value Point
		var bucket common.Time
		if err := rows.Scan(&bucket, &value.UpBytes, &value.DownBytes); err != nil {
			return nil, err
		}
		value.Bucket = bucket.Time
		value.TotalBytes = value.UpBytes + value.DownBytes
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Service) Summary(ctx context.Context, filter Filter) (Summary, error) {
	filter.Interval = "hour"
	filter, err := normalizeFilter(filter)
	if err != nil {
		return Summary{}, err
	}
	where, args := totalWhere(filter)
	var value Summary
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(up_bytes),0),COALESCE(SUM(down_bytes),0),COALESCE(SUM(sample_count),0) FROM traffic_hourly`+where, args...).Scan(&value.UpBytes, &value.DownBytes, &value.Samples)
	value.TotalBytes = value.UpBytes + value.DownBytes
	return value, err
}

func (s *Service) Rankings(ctx context.Context, filter Filter, dimension string, limit int) ([]Ranking, error) {
	filter.Interval = "hour"
	filter, err := normalizeFilter(filter)
	if err != nil {
		return nil, err
	}
	column, nameExpr := "", ""
	switch dimension {
	case "user":
		filter.ProtocolID = ""
		filter.DeviceID = ""
		column, nameExpr = "user_id", `COALESCE((SELECT name FROM users WHERE id=traffic_hourly.user_id),'unassigned')`
	case "protocol":
		filter.UserID = ""
		filter.DeviceID = ""
		column, nameExpr = "protocol_id", `COALESCE((SELECT name FROM protocols WHERE id=traffic_hourly.protocol_id),'unassigned')`
	case "device":
		filter.ProtocolID = ""
		column = "device_id"
		if filter.UserID != "" {
			nameExpr = `COALESCE((SELECT name FROM devices WHERE id=traffic_hourly.device_id), traffic_hourly.device_id)`
		} else {
			nameExpr = `COALESCE((SELECT d.name || ' (' || u.name || ')' FROM devices d JOIN users u ON d.user_id=u.id WHERE d.id=traffic_hourly.device_id), (SELECT name FROM devices WHERE id=traffic_hourly.device_id), traffic_hourly.device_id)`
		}
	default:
		return nil, fmt.Errorf("%w: 维度必须是 user、protocol 或 device", ErrInvalidRequest)
	}
	if limit < 1 || limit > 100 {
		limit = 10
	}
	where, args := queryWhere(filter)
	where += ` AND ` + column + ` != ''`
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT %s,%s,SUM(up_bytes),SUM(down_bytes) FROM traffic_hourly%s GROUP BY %s ORDER BY SUM(up_bytes+down_bytes) DESC LIMIT ?`, column, nameExpr, where, column)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Ranking{}
	for rows.Next() {
		var value Ranking
		if err := rows.Scan(&value.ID, &value.Name, &value.UpBytes, &value.DownBytes); err != nil {
			return nil, err
		}
		value.TotalBytes = value.UpBytes + value.DownBytes
		values = append(values, value)
	}
	return values, rows.Err()
}

// ActiveUsers returns the per-bucket distinct-user trend and the range-wide
// distinct-user count. It reads the per-user rows, so a protocol filter is not
// applicable (user stats carry no protocol attribution) and is dropped.
func (s *Service) ActiveUsers(ctx context.Context, filter Filter) (ActiveUsersResult, error) {
	filter.ProtocolID = ""
	filter, err := normalizeFilter(filter)
	if err != nil {
		return ActiveUsersResult{}, err
	}
	table := "traffic_hourly"
	if filter.Interval == "day" {
		if err := s.AggregateRecent(ctx); err != nil {
			return ActiveUsersResult{}, err
		}
		table = "traffic_daily"
	}
	where, args := queryWhere(filter)
	where += ` AND user_id != ''`
	rows, err := s.db.QueryContext(ctx, `SELECT bucket_start,COUNT(DISTINCT user_id) FROM `+table+where+` GROUP BY bucket_start ORDER BY bucket_start`, args...)
	if err != nil {
		return ActiveUsersResult{}, err
	}
	defer rows.Close()
	result := ActiveUsersResult{Items: []ActiveUserPoint{}}
	for rows.Next() {
		var value ActiveUserPoint
		var bucket common.Time
		if err := rows.Scan(&bucket, &value.Users); err != nil {
			return ActiveUsersResult{}, err
		}
		value.Bucket = bucket.Time
		result.Items = append(result.Items, value)
	}
	if err := rows.Err(); err != nil {
		return ActiveUsersResult{}, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM `+table+where, args...).Scan(&result.TotalUsers)
	return result, err
}

// ProtocolShares returns each protocol's share of inbound-attributed traffic.
// It reads the per-inbound rows, so a user filter is not applicable (inbound
// stats carry no user attribution) and is dropped.
func (s *Service) ProtocolShares(ctx context.Context, filter Filter) ([]ProtocolShare, error) {
	filter.UserID = ""
	filter.Interval = "hour"
	filter, err := normalizeFilter(filter)
	if err != nil {
		return nil, err
	}
	where, args := queryWhere(filter)
	where += ` AND protocol_id != ''`
	query := `SELECT protocol_id,COALESCE((SELECT name FROM protocols WHERE id=traffic_hourly.protocol_id),'unassigned'),SUM(up_bytes),SUM(down_bytes)
FROM traffic_hourly` + where + ` GROUP BY protocol_id ORDER BY SUM(up_bytes+down_bytes) DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ProtocolShare{}
	for rows.Next() {
		var value ProtocolShare
		if err := rows.Scan(&value.ID, &value.Name, &value.UpBytes, &value.DownBytes); err != nil {
			return nil, err
		}
		value.TotalBytes = value.UpBytes + value.DownBytes
		values = append(values, value)
	}
	return values, rows.Err()
}
