package observability

const SchemaVersion = 11

// ProbeSchemaVersion 取全局下一个空闲值（勿与其它包共用：条件迁移判定会
// 恒真，见 internal/database/migrate.go 的编号纪律）。probe_results 属可
// 观测域数据（internal/probe 写、告警评估读），DDL 因此归本包——probe 包
// 依赖 service 层模型，若由 database 直接 import 会与用户/编译器测试成环。
const ProbeSchemaVersion = 13

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

// ProbeSchema 落库每协议最近一次端到端拨测结果（一行一协议，upsert 覆盖）。
// 不复用 runtime_artifacts：它每次内核同步整表 DELETE 重建，会冲掉拨测结果。
const ProbeSchema = `
CREATE TABLE IF NOT EXISTS probe_results (
  protocol_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  latency_ms INTEGER,
  error TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL,
  FOREIGN KEY (protocol_id) REFERENCES protocols(id) ON DELETE CASCADE
);
`
