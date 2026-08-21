package resource

const SchemaVersion = 11

const Schema = `
CREATE TABLE IF NOT EXISTS control_plane_secrets (
  name TEXT PRIMARY KEY,
  value BLOB NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  quota_bytes INTEGER NOT NULL DEFAULT 0 CHECK (quota_bytes >= 0),
  used_bytes INTEGER NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
  expires_at INTEGER,
  reset_interval_days INTEGER NOT NULL DEFAULT 0 CHECK (reset_interval_days >= 0),
  next_reset_at INTEGER,
  -- 用户级订阅链接 token（设备由客户端导入链接时按 UA 自动创建）
  subscription_token_hash BLOB UNIQUE,
  subscription_token_ciphertext BLOB,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- 设备 = 一份独立凭据（uuid/password/订阅 token），入站用户 name 使用设备
-- id（V2Ray stats 据此按设备归因流量）；额度仍按用户汇总（devices.used_bytes
-- 是设备维度累计，users.used_bytes 是用户维度汇总）。
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  used_bytes INTEGER NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
  -- ua_key 非空=由订阅 UA 指纹自动发现的设备；空=历史手动设备。
  ua_key TEXT NOT NULL DEFAULT '',
  source_ip TEXT NOT NULL DEFAULT '',
  platform TEXT NOT NULL DEFAULT 'unknown',
  client TEXT NOT NULL DEFAULT '',
  client_version TEXT NOT NULL DEFAULT '',
  last_seen_at INTEGER,
  token_hash BLOB UNIQUE,
  token_ciphertext BLOB,
  identity_uuid TEXT,
  password_ciphertext BLOB,
  rotated_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (user_id, name),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- idx_devices_user_ua_ip 由 database.Migrate 的 migrateDeviceSourceIP 统一创建：
-- 它引用的 source_ip 列对旧库靠 ALTER TABLE 补齐，若在此处建索引会在补列前执行，
-- 旧库启动即报 no such column: source_ip。

CREATE TABLE IF NOT EXISTS certificates (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  source TEXT NOT NULL DEFAULT 'manual',
  domain TEXT NOT NULL DEFAULT '',
  certificate_pem TEXT NOT NULL,
  private_key_ciphertext BLOB NOT NULL,
  fingerprint TEXT NOT NULL,
  not_before INTEGER NOT NULL,
  not_after INTEGER NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS protocols (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  type TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  listen TEXT NOT NULL DEFAULT '::',
  listen_port INTEGER NOT NULL CHECK (listen_port BETWEEN 1 AND 65535),
  settings_json TEXT NOT NULL DEFAULT '{}',
  certificate_id TEXT,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (certificate_id) REFERENCES certificates(id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_protocols_transport_port
  ON protocols (
    listen,
    listen_port,
    CASE WHEN type IN ('hysteria2','tuic') THEN 'udp' ELSE 'tcp' END
  ) WHERE enabled = 1;

CREATE TABLE IF NOT EXISTS policies (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  default_action TEXT NOT NULL DEFAULT 'direct',
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS rules (
  id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL,
  conditions_json TEXT NOT NULL DEFAULT '{}',
  action TEXT NOT NULL,
  no_resolve INTEGER NOT NULL DEFAULT 0,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (policy_id, priority),
  FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS user_protocols (
  user_id TEXT NOT NULL,
  protocol_id TEXT NOT NULL,
  PRIMARY KEY (user_id, protocol_id),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (protocol_id) REFERENCES protocols(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS user_policies (
  user_id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS singbox_config (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  log_level TEXT NOT NULL DEFAULT 'info',
  log_timestamp INTEGER NOT NULL DEFAULT 1,
  dns_servers_json TEXT NOT NULL DEFAULT '[]',
  dns_final TEXT NOT NULL DEFAULT '',
  clash_api_enabled INTEGER NOT NULL DEFAULT 1,
  clash_api_listen TEXT NOT NULL DEFAULT '127.0.0.1:9090',
  clash_api_secret_ciphertext BLOB,
  v2ray_api_enabled INTEGER NOT NULL DEFAULT 1,
  v2ray_api_listen TEXT NOT NULL DEFAULT '127.0.0.1:10000',
  default_outbound_tag TEXT NOT NULL DEFAULT 'direct',
  revision INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS singbox_outbounds (
  id TEXT PRIMARY KEY,
  tag TEXT NOT NULL COLLATE NOCASE UNIQUE,
  type TEXT NOT NULL,
  server TEXT NOT NULL DEFAULT '',
  server_port INTEGER NOT NULL DEFAULT 0 CHECK (server_port BETWEEN 0 AND 65535),
  username TEXT NOT NULL DEFAULT '',
  password_ciphertext BLOB,
  settings_json BLOB,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS singbox_route_rules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  priority INTEGER NOT NULL,
  conditions_json TEXT NOT NULL DEFAULT '{}',
  outbound_tag TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (priority)
);

CREATE TABLE IF NOT EXISTS singbox_rule_sets (
  id TEXT PRIMARY KEY,
  tag TEXT NOT NULL COLLATE NOCASE UNIQUE,
  format TEXT NOT NULL,
  url TEXT NOT NULL,
  download_detour TEXT NOT NULL DEFAULT '',
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runtime_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  config_digest TEXT NOT NULL DEFAULT '',
  last_applied_at INTEGER,
  last_failed_at INTEGER,
  failure_reason TEXT NOT NULL DEFAULT '',
  artifact_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS runtime_artifacts (
  user_id TEXT NOT NULL,
  device_id TEXT NOT NULL DEFAULT '',
  format TEXT NOT NULL,
  content BLOB NOT NULL,
  digest TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, device_id, format),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (device_id) REFERENCES devices(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS seed_markers (
  name TEXT PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_status_name ON users (enabled, name);
CREATE INDEX IF NOT EXISTS idx_rules_policy_priority ON rules (policy_id, priority);
CREATE INDEX IF NOT EXISTS idx_singbox_route_rules_priority ON singbox_route_rules (priority);
CREATE INDEX IF NOT EXISTS idx_runtime_artifacts_user ON runtime_artifacts (user_id);
CREATE INDEX IF NOT EXISTS idx_devices_user ON devices (user_id);
`
