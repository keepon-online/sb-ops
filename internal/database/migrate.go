package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/events"
	"github.com/keepon-online/sb-ops/internal/observability"
	"github.com/keepon-online/sb-ops/internal/service"
	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	"github.com/keepon-online/sb-ops/internal/traffic"
)

const identitySchemaVersion = 1

// CurrentSchemaVersion is the highest schema version recorded after a fresh
// migration. It reflects the single-VPS layout (agent/node tables removed).
const CurrentSchemaVersion = observability.SchemaVersion

// dropAgentSchema removes the legacy agent/node/rollout tables. These are
// idempotent DROPs so fresh databases (which never had them) are unaffected.
const dropAgentSchema = `
DROP TABLE IF EXISTS release_targets;
DROP TABLE IF EXISTS release_rollouts;
DROP TABLE IF EXISTS node_task_attempts;
DROP TABLE IF EXISTS node_tasks;
DROP TABLE IF EXISTS node_heartbeats;
DROP TABLE IF EXISTS node_certificates;
DROP TABLE IF EXISTS enrollment_tokens;
DROP TABLE IF EXISTS control_plane_ca;
DROP TABLE IF EXISTS nodes;
`

// dropRbacSchema removes the role/permission tables from the single-admin
// layout. account_roles is dropped first because it references roles.
const dropRbacSchema = `
DROP TABLE IF EXISTS account_roles;
DROP TABLE IF EXISTS roles;
`

// dropSettingsSchema removes the panel-managed settings table, which was
// folded into config.yaml (notifications and traffic retention).
const dropSettingsSchema = `
DROP TABLE IF EXISTS settings;
`

// dropResourceRevisionsSchema removes the resource revision history table,
// which was never written to or read by any code path.
const dropResourceRevisionsSchema = `
DROP TABLE IF EXISTS resource_revisions;
`

func Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始迁移: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, identitySchema); err != nil {
		return fmt.Errorf("应用 identity schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropRbacSchema); err != nil {
		return fmt.Errorf("删除 RBAC 旧表失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropSettingsSchema); err != nil {
		return fmt.Errorf("删除 settings 旧表失败: %w", err)
	}
	if err := dropLegacyRuntimeSchema(ctx, tx); err != nil {
		return fmt.Errorf("移除旧版 release schema: %w", err)
	}
	if err := dropPreLaunchSchemas(ctx, tx); err != nil {
		return fmt.Errorf("重建上线前变更表: %w", err)
	}
	if _, err := tx.ExecContext(ctx, resource.Schema); err != nil {
		return fmt.Errorf("应用 resource schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, serviceargo.Schema); err != nil {
		return fmt.Errorf("应用 argo schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropResourceRevisionsSchema); err != nil {
		return fmt.Errorf("删除 resource_revisions 旧表失败: %w", err)
	}
	if err := seedDefaultRuleSets(ctx, tx); err != nil {
		return fmt.Errorf("写入默认规则集: %w", err)
	}
	if err := migrateCertificateSource(ctx, tx); err != nil {
		return fmt.Errorf("迁移证书来源字段: %w", err)
	}
	if err := migrateOutboundSettings(ctx, tx); err != nil {
		return fmt.Errorf("迁移出站扩展配置: %w", err)
	}
	if err := migrateDeviceUAKey(ctx, tx); err != nil {
		return fmt.Errorf("迁移设备 UA 识别列: %w", err)
	}
	if err := migrateDeviceMetadata(ctx, tx); err != nil {
		return fmt.Errorf("迁移设备元数据列: %w", err)
	}
	if err := migrateUserSubscriptionToken(ctx, tx); err != nil {
		return fmt.Errorf("迁移用户订阅令牌列: %w", err)
	}
	if err := migrateUserPolicies(ctx, tx); err != nil {
		return fmt.Errorf("迁移订阅策略: %w", err)
	}
	if err := resetManagedSingBoxConfig(ctx, tx); err != nil {
		return fmt.Errorf("重置托管 sing-box 配置: %w", err)
	}
	if _, err := tx.ExecContext(ctx, traffic.Schema); err != nil {
		return fmt.Errorf("应用 traffic schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, events.Schema); err != nil {
		return fmt.Errorf("应用 event schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, observability.Schema); err != nil {
		return fmt.Errorf("应用 observability schema: %w", err)
	}
	// Drop the legacy agent/node/rollout tables. With foreign keys enforced,
	// the node-side tables reference each other, so dropping them before the
	// traffic tables is safe only because traffic was redeclared above without
	// its node_id foreign keys. For pre-existing traffic tables that still
	// carry the old FK, rebuild them without the constraint.
	if err := migrateTrafficForeignKeys(ctx, tx); err != nil {
		return fmt.Errorf("重建 traffic 表: %w", err)
	}
	if _, err := tx.ExecContext(ctx, dropAgentSchema); err != nil {
		return fmt.Errorf("移除旧版 agent schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at) VALUES (?, ?)`,
		identitySchemaVersion, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("记录 schema 版本: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at) VALUES (?, ?)`,
		resource.SchemaVersion, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("记录 resource schema 版本: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at) VALUES (?, ?)`,
		traffic.SchemaVersion, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("记录 traffic schema 版本: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at) VALUES (?, ?)`,
		events.SchemaVersion, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("记录 event schema 版本: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_versions (version, applied_at) VALUES (?, ?)`,
		observability.SchemaVersion, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("记录 observability schema 版本: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移: %w", err)
	}
	return nil
}

// aiRuleSetSeedVersion gates the one-time top-up of AI unlock rule sets on
// databases created before gemini/anthropic existed. After the marker is
// recorded, later migrations never resurrect deliberately deleted entries.
const aiRuleSetSeedVersion = "seed-rulesets-ai-1"

// seedDefaultRuleSets pre-populates the rule set library with common
// open-source sing-box rule sets (official SagerNet sing-geosite/sing-geoip).
// Fresh databases get the full list; existing ones receive a one-time top-up
// (marker-gated) so later reinstalls never resurrect rule sets the
// administrator deliberately deleted.
func seedDefaultRuleSets(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM singbox_rule_sets`).Scan(&count); err != nil {
		return err
	}
	now := time.Now().Unix()
	if count == 0 {
		for _, ruleSet := range defaultRuleSets {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO singbox_rule_sets(id,tag,format,url,download_detour,revision,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`,
				ruleSet.ID, ruleSet.Tag, ruleSet.Format, ruleSet.URL, ruleSet.DownloadDetour, now, now); err != nil {
				return err
			}
		}
	}
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM seed_markers WHERE name=?`, aiRuleSetSeedVersion).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	for _, canonical := range servicesingbox.CanonicalAIRuleSets {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO singbox_rule_sets(id,tag,format,url,download_detour,revision,created_at,updated_at) VALUES(?,?,'binary',?,'',1,?,?)`,
			"seed-"+canonical.Tag, canonical.Tag, canonical.URL, now, now); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO seed_markers (name, applied_at) VALUES (?, ?)`, aiRuleSetSeedVersion, now)
	return err
}

var defaultRuleSets = func() []struct {
	ID, Tag, Format, URL, DownloadDetour string
} {
	seeds := []struct {
		ID, Tag, Format, URL, DownloadDetour string
	}{
		{ID: "seed-ads", Tag: "ads", Format: "binary", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs"},
		{ID: "seed-google", Tag: "google", Format: "binary", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google.srs"},
		{ID: "seed-telegram", Tag: "telegram", Format: "binary", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-telegram.srs"},
		{ID: "seed-cn-domains", Tag: "cn-domains", Format: "binary", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-cn.srs"},
		{ID: "seed-cn-ip", Tag: "cn-ip", Format: "binary", URL: "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs"},
	}
	for _, canonical := range servicesingbox.CanonicalAIRuleSets {
		seeds = append(seeds, struct {
			ID, Tag, Format, URL, DownloadDetour string
		}{ID: "seed-" + canonical.Tag, Tag: canonical.Tag, Format: "binary", URL: canonical.URL})
	}
	return seeds
}()

// dropLegacyRuntimeSchema clears retired template and release data before
// applying the current runtime synchronization schema.
func dropLegacyRuntimeSchema(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name IN ('templates', 'releases'))`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
DROP TABLE IF EXISTS release_artifacts;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS template_versions;
DROP TABLE IF EXISTS templates;
`)
	return err
}

func resetManagedSingBoxConfig(ctx context.Context, tx *sql.Tx) error {
	var columnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info('singbox_config') WHERE name='clash_api_secret_ciphertext'`).Scan(&columnCount); err != nil {
		return err
	}
	if columnCount == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE singbox_config ADD COLUMN clash_api_secret_ciphertext BLOB`); err != nil {
			return err
		}
	}
	var applied int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_versions WHERE version=?`, resource.SchemaVersion).Scan(&applied)
	if err != nil || applied > 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM singbox_config`)
	return err
}

// dropPreLaunchSchemas removes pre-launch table shapes that changed
// incompatibly (credentials → devices, per-device runtime artifacts,
// device-dimension traffic rows). The product has not shipped, so affected
// tables are dropped and recreated empty instead of being data-migrated;
// the DROPs are conditional on the old shape so fresh databases are
// unaffected and the step becomes a no-op once every database is new.
func dropPreLaunchSchemas(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS credentials`); err != nil {
		return err
	}
	if err := dropTableIfMissingColumn(ctx, tx, "runtime_artifacts", "device_id"); err != nil {
		return err
	}
	// 计算旧形状哨兵：traffic_hourly 表存在且缺 device_id 列 → 旧库，
	// 需要重建全部 traffic 表；否则一块更新无需重建。
	needTrafficDrop := false
	hourlyExists, err := tableExists(ctx, tx, "traffic_hourly")
	if err != nil {
		return err
	}
	if hourlyExists {
		hasColumn, cerr := tableHasColumn(ctx, tx, "traffic_hourly", "device_id")
		if cerr != nil {
			return cerr
		}
		needTrafficDrop = !hasColumn
	}
	if needTrafficDrop {
		for _, table := range []string{"traffic_hourly", "traffic_daily", "traffic_cursors", "traffic_batches", "traffic_boot_sequences", "aggregate_watermarks"} {
			if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
				return err
			}
		}
	}
	return nil
}

func dropTableIfMissingColumn(ctx context.Context, tx *sql.Tx, table, column string) error {
	exists, err := tableExists(ctx, tx, table)
	if err != nil || !exists {
		return err
	}
	hasColumn, err := tableHasColumn(ctx, tx, table, column)
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = tx.ExecContext(ctx, `DROP TABLE `+table)
	return err
}

func tableExists(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
	return count > 0, err
}

func tableHasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count)
	return count > 0, err
}

// migrateDeviceUAKey adds the user-agent fingerprint column used to route
// subscription downloads to auto-discovered devices. ua_key is empty for
// manually managed devices (including the default device, whose token doubles
// as the user-level link), so the uniqueness guard only covers auto devices.
func migrateDeviceUAKey(ctx context.Context, tx *sql.Tx) error {
	var columnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info('devices') WHERE name='ua_key'`).Scan(&columnCount); err != nil {
		return err
	}
	if columnCount == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE devices ADD COLUMN ua_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_user_ua ON devices (user_id, ua_key) WHERE ua_key != ''`)
	return err
}

// migrateUserSubscriptionToken adds the user-level subscription link columns.
// Tokens are lazily issued on first read for users created before the columns
// existed, so no backfill is needed.
func migrateUserSubscriptionToken(ctx context.Context, tx *sql.Tx) error {
	for column, ddl := range map[string]string{
		"subscription_token_hash":       `ALTER TABLE users ADD COLUMN subscription_token_hash BLOB`,
		"subscription_token_ciphertext": `ALTER TABLE users ADD COLUMN subscription_token_ciphertext BLOB`,
	} {
		present, err := tableHasColumn(ctx, tx, "users", column)
		if err != nil {
			return err
		}
		if !present {
			if _, err := tx.ExecContext(ctx, ddl); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_subscription_token ON users (subscription_token_hash) WHERE subscription_token_hash IS NOT NULL`)
	return err
}

func migrateDeviceMetadata(ctx context.Context, tx *sql.Tx) error {
	for column, ddl := range map[string]string{
		"platform":       `ALTER TABLE devices ADD COLUMN platform TEXT NOT NULL DEFAULT 'unknown'`,
		"client":         `ALTER TABLE devices ADD COLUMN client TEXT NOT NULL DEFAULT ''`,
		"client_version": `ALTER TABLE devices ADD COLUMN client_version TEXT NOT NULL DEFAULT ''`,
		"last_seen_at":   `ALTER TABLE devices ADD COLUMN last_seen_at INTEGER`,
	} {
		present, err := tableHasColumn(ctx, tx, "devices", column)
		if err != nil {
			return err
		}
		if !present {
			if _, err := tx.ExecContext(ctx, ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// migrateOutboundSettings adds the encrypted per-type settings blob column to
// the outbound table. SOCKS rows keep using the legacy columns; WARP rows
// store their wireguard parameters (including the private key) encrypted here.
func migrateOutboundSettings(ctx context.Context, tx *sql.Tx) error {
	var columnCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info('singbox_outbounds') WHERE name='settings_json'`).Scan(&columnCount); err != nil {
		return err
	}
	if columnCount == 0 {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE singbox_outbounds ADD COLUMN settings_json BLOB`); err != nil {
			return err
		}
	}
	return nil
}

// migrateCertificateSource adds the source/domain columns that track whether a
// certificate is self-signed, manually imported, or ACME-issued.
func migrateCertificateSource(ctx context.Context, tx *sql.Tx) error {
	for column, ddl := range map[string]string{
		"source": `ALTER TABLE certificates ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'`,
		"domain": `ALTER TABLE certificates ADD COLUMN domain TEXT NOT NULL DEFAULT ''`,
	} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM pragma_table_info('certificates') WHERE name=?`, column).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.ExecContext(ctx, ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func migrateUserPolicies(ctx context.Context, tx *sql.Tx) error {
	var ddl string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='user_policies'`).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) || strings.Contains(ddl, "user_id TEXT PRIMARY KEY") {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE user_policies__single (
  user_id TEXT PRIMARY KEY,
  policy_id TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (policy_id) REFERENCES policies(id) ON DELETE RESTRICT
);
INSERT INTO user_policies__single(user_id,policy_id)
SELECT up.user_id,up.policy_id
FROM user_policies up
JOIN policies p ON p.id=up.policy_id
WHERE NOT EXISTS (
  SELECT 1 FROM user_policies later
  JOIN policies later_policy ON later_policy.id=later.policy_id
  WHERE later.user_id=up.user_id
    AND (later_policy.name COLLATE NOCASE > p.name COLLATE NOCASE OR (later_policy.name COLLATE NOCASE = p.name COLLATE NOCASE AND later.policy_id > up.policy_id))
);
DROP TABLE user_policies;
ALTER TABLE user_policies__single RENAME TO user_policies;
`); err != nil {
		return err
	}
	return nil
}

// migrateTrafficForeignKeys rebuilds the traffic tables without their node_id
// foreign keys when an upgrade from the agent era is detected. SQLite cannot
// drop constraints in place, so the affected tables are recreated via the
// standard temporary-table swap pattern.
func migrateTrafficForeignKeys(ctx context.Context, tx *sql.Tx) error {
	hasLegacyFK, err := trafficTableReferencesNodes(ctx, tx)
	if err != nil {
		return err
	}
	if !hasLegacyFK {
		return nil
	}
	tables := []string{"traffic_batches", "traffic_boot_sequences", "traffic_cursors", "traffic_hourly", "traffic_daily"}
	for _, table := range tables {
		def, ok := trafficTableDefinitions[table]
		if !ok {
			return fmt.Errorf("缺少表定义: %s", table)
		}
		if err := rebuildTable(ctx, tx, table, def); err != nil {
			return err
		}
	}
	return nil
}

func trafficTableReferencesNodes(ctx context.Context, tx *sql.Tx) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='traffic_batches'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var ddl string
		if err := rows.Scan(&ddl); err != nil {
			return false, err
		}
		if strings.Contains(ddl, "REFERENCES nodes") {
			return true, nil
		}
	}
	return false, rows.Err()
}

// rebuildTable recreates a table from its current rows using a fresh, FK-free
// definition. Columns are projected by position from the canonical DDL.
func rebuildTable(ctx context.Context, tx *sql.Tx, name, definition string) error {
	tmp := name + "__sb_rebuild"
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+tmp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s %s`, tmp, definition)); err != nil {
		return err
	}
	columns := trafficTableColumns[name]
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s`, tmp, columns, columns, name)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE `+name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, tmp, name)); err != nil {
		return err
	}
	return nil
}

// trafficTableDefinitions mirror traffic/schema.go but without the legacy
// node_id foreign keys. Column order must stay identical to the original DDL
// so the row copy projects cleanly.
var trafficTableDefinitions = map[string]string{
	"traffic_batches": `(
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  payload_hash TEXT NOT NULL,
  collected_at INTEGER NOT NULL,
  received_at INTEGER NOT NULL,
  UNIQUE(node_id, boot_id, sequence)
)`,
	"traffic_boot_sequences": `(
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  last_sequence INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(node_id, boot_id)
)`,
	"traffic_cursors": `(
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
)`,
	"traffic_hourly": `(
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
)`,
	"traffic_daily": `(
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
)`,
}

// trafficTableColumns lists each table's columns in declaration order for the
// row copy during the FK-free rebuild.
var trafficTableColumns = map[string]string{
	"traffic_batches":        "id,node_id,boot_id,sequence,payload_hash,collected_at,received_at",
	"traffic_boot_sequences": "node_id,boot_id,last_sequence,updated_at",
	"traffic_cursors":        "node_id,boot_id,stat_key,user_id,protocol_id,device_id,up_bytes,down_bytes,sequence,updated_at",
	"traffic_hourly":         "bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at",
	"traffic_daily":          "bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at",
}

const identitySchema = `
CREATE TABLE IF NOT EXISTS schema_versions (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL COLLATE NOCASE UNIQUE,
  password_hash BLOB NOT NULL,
  enabled INTEGER NOT NULL,
  must_change_password INTEGER NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS setup_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  initialized_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO setup_state (id, initialized_at)
SELECT 1, created_at FROM accounts ORDER BY created_at LIMIT 1;

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL,
  token_hash BLOB NOT NULL UNIQUE,
  client_ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER,
  created_at INTEGER NOT NULL,
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sessions_account_active
  ON sessions (account_id, revoked_at, expires_at);

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
