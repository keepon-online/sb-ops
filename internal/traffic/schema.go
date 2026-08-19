package traffic

// 行模型（互斥）：用户×设备行（user_id≠”、device_id≠”、protocol_id=”）与
// 入站行（user_id=”、device_id=”、protocol_id≠”）。设备行之和 = 用户总量。
const SchemaVersion = 7

const Schema = `
CREATE TABLE IF NOT EXISTS traffic_batches (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  payload_hash TEXT NOT NULL,
  collected_at INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  UNIQUE(node_id, boot_id, sequence)
);
CREATE TABLE IF NOT EXISTS traffic_boot_sequences (
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  last_sequence INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(node_id, boot_id)
);
CREATE TABLE IF NOT EXISTS traffic_cursors (
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  stat_key TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  protocol_id TEXT NOT NULL DEFAULT '',
  device_id TEXT NOT NULL DEFAULT '',
  up_bytes INTEGER NOT NULL,
  down_bytes INTEGER NOT NULL,
  sequence INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(node_id, boot_id, stat_key)
);
CREATE TABLE IF NOT EXISTS traffic_hourly (
  bucket_start INTEGER NOT NULL,
  node_id TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  protocol_id TEXT NOT NULL DEFAULT '',
  device_id TEXT NOT NULL DEFAULT '',
  up_bytes INTEGER NOT NULL DEFAULT 0,
  down_bytes INTEGER NOT NULL DEFAULT 0,
  sample_count INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(bucket_start,node_id,user_id,protocol_id,device_id)
);
CREATE TABLE IF NOT EXISTS traffic_daily (
  bucket_start INTEGER NOT NULL,
  node_id TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  protocol_id TEXT NOT NULL DEFAULT '',
  device_id TEXT NOT NULL DEFAULT '',
  up_bytes INTEGER NOT NULL DEFAULT 0,
  down_bytes INTEGER NOT NULL DEFAULT 0,
  sample_count INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(bucket_start,node_id,user_id,protocol_id,device_id)
);
CREATE TABLE IF NOT EXISTS aggregate_watermarks (
  name TEXT PRIMARY KEY,
  completed_through INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_traffic_hourly_range ON traffic_hourly(bucket_start,node_id,user_id,protocol_id,device_id);
CREATE INDEX IF NOT EXISTS idx_traffic_daily_range ON traffic_daily(bucket_start,node_id,user_id,protocol_id,device_id);
`
