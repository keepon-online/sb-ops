package events

const SchemaVersion = 9

const Schema = `
CREATE TABLE IF NOT EXISTS system_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  scope TEXT NOT NULL,
  resource_id TEXT NOT NULL DEFAULT '',
  permission TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT UNIQUE,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_system_events_created ON system_events(created_at,id);
`
