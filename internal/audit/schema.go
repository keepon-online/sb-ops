package audit

// Schema 是审计日志表的建表语句；表的所有权在本包（v0.4 起从 identity
// schema 迁出，identity 只保留账号与会话）。
const Schema = `
CREATE TABLE IF NOT EXISTS audit_logs (
  id TEXT PRIMARY KEY,
  actor_id TEXT,
  action TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL DEFAULT '',
  result TEXT NOT NULL,
  request_id TEXT NOT NULL DEFAULT '',
  client_ip TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  FOREIGN KEY (actor_id) REFERENCES accounts(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor_created ON audit_logs (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_resource_created ON audit_logs (resource_type, resource_id, created_at DESC);
`

// SchemaVersion 用于 schema_versions 的一次性迁移判定：迁入本包时对旧表
// 做一次破坏重建（清除旧版噪音——成功登录、runtime.sync、已废弃的读操作
// 审计；线上数据确认不保留），此后启动走 IF NOT EXISTS 幂等。
// 注意 schema_versions 的 version 是全局唯一编号（identity=1、traffic=7、
// events=9、argo=10、resource/observability=11），新迁移取下一个空闲值，
// 撞号会让「已迁移」判定恒真、迁移体永远不执行（首版 audit=1 撞 identity
// 即此 bug）。CurrentSchemaVersion 须同步指向新的最大值。
const SchemaVersion = 12
