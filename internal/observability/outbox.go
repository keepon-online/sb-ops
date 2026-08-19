package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	controlevents "github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Notification struct {
	ID        string
	AlertID   string
	EventType string
	Payload   json.RawMessage
	Attempts  int
}

type NotificationStats struct {
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
}

type Notifier interface {
	Notify(context.Context, Notification) error
}

type EventNotifier struct{ bus *controlevents.Bus }

func NewEventNotifier(bus *controlevents.Bus) *EventNotifier { return &EventNotifier{bus: bus} }

func (n *EventNotifier) Notify(ctx context.Context, value Notification) error {
	var payload any
	if err := json.Unmarshal(value.Payload, &payload); err != nil {
		return err
	}
	_, err := n.bus.Publish(ctx, controlevents.Input{
		Type: value.EventType, Scope: "alert", ResourceID: value.AlertID, Permission: "settings:read", IdempotencyKey: value.ID, Payload: payload,
	})
	return err
}

func enqueueNotification(ctx context.Context, tx *sql.Tx, alert Alert, eventType string, now time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"id": alert.ID, "type": alert.Type, "severity": alert.Severity, "message": alert.Message, "status": alert.Status,
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_outbox(id,alert_id,event_type,payload_json,attempts,available_at,created_at) VALUES(?,?,?,?,0,?,?)`,
		uuid.NewString(), alert.ID, eventType, string(payload), common.UnixTime(now), common.UnixTime(now))
	return err
}

func (s *Service) Dispatch(ctx context.Context) error {
	if s.notifier == nil {
		return nil
	}
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `
SELECT o.id,o.alert_id,o.event_type,o.payload_json,o.attempts
FROM notification_outbox o
WHERE o.delivered_at IS NULL AND o.failed_at IS NULL AND o.available_at<=?
  AND NOT EXISTS (
    SELECT 1 FROM notification_outbox earlier
    WHERE earlier.alert_id=o.alert_id AND earlier.created_at<o.created_at
      AND earlier.delivered_at IS NULL AND earlier.failed_at IS NULL
  )
ORDER BY o.available_at,o.id LIMIT 100`, common.UnixTime(now))
	if err != nil {
		return err
	}
	values := []Notification{}
	for rows.Next() {
		var value Notification
		var payload string
		if err := rows.Scan(&value.ID, &value.AlertID, &value.EventType, &payload, &value.Attempts); err != nil {
			rows.Close()
			return err
		}
		value.Payload = json.RawMessage(payload)
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, value := range values {
		if err := s.notifier.Notify(ctx, value); err != nil {
			if updateErr := s.recordNotificationFailure(ctx, value, err, now); updateErr != nil {
				return updateErr
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET delivered_at=?,last_error='' WHERE id=? AND delivered_at IS NULL`, common.UnixTime(now), value.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordNotificationFailure(ctx context.Context, value Notification, notifyErr error, now time.Time) error {
	attempts := value.Attempts + 1
	message := strings.TrimSpace(notifyErr.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	if attempts >= 10 {
		_, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET attempts=?,last_error=?,failed_at=? WHERE id=?`, attempts, message, common.UnixTime(now), value.ID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET attempts=?,last_error=?,available_at=? WHERE id=?`, attempts, message, common.UnixTime(now.Add(notificationBackoff(attempts))), value.ID)
	return err
}

func notificationBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 5 * time.Second
	case 2:
		return 30 * time.Second
	case 3:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func (s *Service) NotificationStats(ctx context.Context) (NotificationStats, error) {
	var value NotificationStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE delivered_at IS NULL AND failed_at IS NULL),COUNT(*) FILTER (WHERE failed_at IS NOT NULL) FROM notification_outbox`).Scan(&value.Pending, &value.Failed)
	return value, err
}

// PurgeNotifications removes finished notifications (delivered or terminally
// failed) older than the cutoff so the outbox table stays bounded. Rows still
// pending retry are never touched.
func (s *Service) PurgeNotifications(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM notification_outbox WHERE (delivered_at IS NOT NULL AND delivered_at<?) OR (failed_at IS NOT NULL AND failed_at<?)`, common.UnixTime(cutoff), common.UnixTime(cutoff))
	if err != nil {
		return 0, err
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}
