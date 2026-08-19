package argo

// Schema declares the Argo tunnel tables. Tunnels ride a supervised
// cloudflared process and expose an existing vmess (WebSocket) inbound
// through a Cloudflare edge domain.
const Schema = `
CREATE TABLE IF NOT EXISTS argo_tunnels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  mode TEXT NOT NULL CHECK (mode IN ('temporary','token')),
  fixed_domain TEXT NOT NULL DEFAULT '',
  preferred_address TEXT NOT NULL DEFAULT '',
  protocol_id TEXT NOT NULL,
  token_ciphertext BLOB,
  assigned_domain TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'stopped',
  last_error TEXT NOT NULL DEFAULT '',
  started_at INTEGER,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY (protocol_id) REFERENCES protocols(id) ON DELETE CASCADE
);

-- 两条启用中的隧道不得承载同一协议：订阅节点名由协议名派生，重复会生成
-- 重复名称的 clash 节点（客户端配置非法）。
CREATE UNIQUE INDEX IF NOT EXISTS idx_argo_tunnels_protocol_enabled
  ON argo_tunnels (protocol_id) WHERE enabled = 1;
`
