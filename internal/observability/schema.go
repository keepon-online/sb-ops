package observability

const SchemaVersion = 11

const Schema = `
CREATE TABLE IF NOT EXISTS operational_alerts (
  id TEXT PRIMARY KEY,
  fingerprint TEXT NOT NULL UNIQUE,
  type TEXT NOT NULL,
  severity TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL DEFAULT '',
  message TEXT NOT NULL,
  status TEXT NOT NULL,
  first_seen_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  acknowledged_at INTEGER,
  acknowledged_by TEXT,
  resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_operational_alerts_status ON operational_alerts(status,severity,last_seen_at DESC);
CREATE TABLE IF NOT EXISTS notification_outbox (
  id TEXT PRIMARY KEY,
  alert_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  available_at INTEGER NOT NULL,
  delivered_at INTEGER,
  last_error TEXT NOT NULL DEFAULT '',
  failed_at INTEGER,
  created_at INTEGER NOT NULL,
  FOREIGN KEY(alert_id) REFERENCES operational_alerts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_notification_outbox_pending ON notification_outbox(delivered_at,failed_at,available_at,id);
`
