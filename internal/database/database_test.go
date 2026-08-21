package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenConfiguresWALAndPrivateFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := Open(Options{Path: path, MMapSize: 4 << 20, Synchronous: "NORMAL"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode=%q, want wal", journalMode)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode=%o, want 600", got)
	}
}

// Windows 盘符绝对路径（D:\x\y.db）一旦拼成 file: URI，盘符会被 SQLite
// 的 URI 解析器当成 authority 拒绝（invalid uri authority，路径中的 \ 也
// 被转义为 %5C）。此测试钉住 DSN 必须保持普通路径 + 查询参数形式。
func TestBuildDSNKeepsPlainPathForWindowsDriveLetter(t *testing.T) {
	got := buildDSN(`D:\project\sb-ops\singbox-web.db`, "NORMAL")
	want := `D:\project\sb-ops\singbox-web.db?_busy_timeout=5000&_foreign_keys=ON&_journal_mode=WAL&_synchronous=NORMAL`
	if got != want {
		t.Fatalf("dsn=%q, want %q", got, want)
	}
}

func TestMigrateCreatesControlPlaneSchemaAndVersion(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_versions`).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != CurrentSchemaVersion {
		t.Fatalf("version=%d, want %d", version, CurrentSchemaVersion)
	}
	for _, column := range []string{"platform", "client", "client_version", "last_seen_at", "source_ip"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('devices') WHERE name=?`, column).Scan(&count); err != nil {
			t.Fatalf("query device column %s: %v", column, err)
		}
		if count != 1 {
			t.Fatalf("device column %s missing", column)
		}
	}

	for _, table := range []string{
		"accounts", "sessions", "audit_logs",
		"users", "devices", "protocols", "certificates", "policies", "rules",
		"runtime_state", "runtime_artifacts",
	} {
		var name string
		if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	for _, table := range []string{"templates", "template_versions", "releases", "release_artifacts", "roles", "account_roles"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("query removed table %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("removed table %s still present", table)
		}
	}
}

func TestMigrateSeedsDefaultRuleSetsOnce(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var seeded int
	if err := db.QueryRow(`SELECT COUNT(1) FROM singbox_rule_sets`).Scan(&seeded); err != nil {
		t.Fatalf("count rule sets: %v", err)
	}
	if seeded != len(defaultRuleSets) {
		t.Fatalf("seeded=%d, want %d", seeded, len(defaultRuleSets))
	}
	// A deleted default must not be resurrected by a later migration.
	if _, err := db.Exec(`DELETE FROM singbox_rule_sets WHERE tag='ads'`); err != nil {
		t.Fatalf("delete ads: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM singbox_rule_sets WHERE tag='ads'`).Scan(&count); err != nil {
		t.Fatalf("count ads: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleted default rule set was re-seeded")
	}
}

// trojan/shadowtls 曾被 removeUnsupportedProtocols 在每次迁移时无条件删除，
// 与面板/编译器的完整支持矛盾（可创建、可运行，重启即静默丢失）。此测试钉住
// 「迁移不得删除受支持协议」的行为，防止回归。
func TestMigrateKeepsTrojanAndShadowTLSProtocols(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, protocolType := range []struct{ kind, port string }{{"trojan", "20443"}, {"shadowtls", "22443"}} {
		if _, err := db.Exec(`INSERT INTO protocols (id,name,type,listen_port,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
			"proto-"+protocolType.kind, "Proto-"+protocolType.kind, protocolType.kind, protocolType.port, "2026-08-15T00:00:00Z", "2026-08-15T00:00:00Z"); err != nil {
			t.Fatalf("insert %s protocol: %v", protocolType.kind, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users (id,name,enabled,created_at,updated_at) VALUES ('u1','u1',1,'2026-08-15T00:00:00Z','2026-08-15T00:00:00Z')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_protocols (user_id,protocol_id) VALUES ('u1','proto-trojan')`); err != nil {
		t.Fatalf("insert user protocol: %v", err)
	}

	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var protocols int
	if err := db.QueryRow(`SELECT COUNT(1) FROM protocols WHERE type IN ('trojan','shadowtls')`).Scan(&protocols); err != nil {
		t.Fatalf("count protocols: %v", err)
	}
	if protocols != 2 {
		t.Fatalf("trojan/shadowtls protocols after Migrate=%d, want 2", protocols)
	}
	var bindings int
	if err := db.QueryRow(`SELECT COUNT(1) FROM user_protocols WHERE protocol_id='proto-trojan'`).Scan(&bindings); err != nil {
		t.Fatalf("count user protocols: %v", err)
	}
	if bindings != 1 {
		t.Fatalf("trojan user binding after Migrate=%d, want 1", bindings)
	}
}

func TestMigrateDiscardsLegacyTemplateData(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const legacySchema = `
CREATE TABLE templates (id TEXT PRIMARY KEY);
CREATE TABLE template_versions (id TEXT PRIMARY KEY, template_id TEXT NOT NULL REFERENCES templates(id));
CREATE TABLE releases (id TEXT PRIMARY KEY, template_version_id TEXT REFERENCES template_versions(id));
CREATE TABLE release_artifacts (id TEXT PRIMARY KEY, release_id TEXT NOT NULL REFERENCES releases(id));
INSERT INTO templates VALUES ('template-1');
INSERT INTO template_versions VALUES ('version-1', 'template-1');
INSERT INTO releases VALUES ('release-1', 'version-1');
INSERT INTO release_artifacts VALUES ('artifact-1', 'release-1');
`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"templates", "template_versions", "releases", "release_artifacts"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("query removed table %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("removed table %s still present", table)
		}
	}
	var stateRows int
	if err := db.QueryRow(`SELECT COUNT(1) FROM runtime_state`).Scan(&stateRows); err != nil {
		t.Fatalf("query runtime state: %v", err)
	}
	if stateRows != 0 {
		t.Fatalf("runtime state rows=%d, want 0 after legacy reset", stateRows)
	}
}

func TestMigrateMarksExistingAccountsAsInitialized(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("initial Migrate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,enabled,must_change_password,revision,created_at,updated_at) VALUES('legacy-admin','admin',X'00',1,0,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM setup_state`); err != nil {
		t.Fatalf("clear setup state: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("upgrade Migrate: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM setup_state WHERE id=1`).Scan(&count); err != nil {
		t.Fatalf("query setup state: %v", err)
	}
	if count != 1 {
		t.Fatalf("setup state count=%d, want 1", count)
	}
}

func TestMigrateRebuildsTrafficTablesWithoutNodeForeignKey(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Simulate an upgrade from the agent era: a traffic_batches table that still
	// references the nodes table, plus the legacy nodes table itself.
	const legacyDDL = `
CREATE TABLE nodes (id TEXT PRIMARY KEY);
CREATE TABLE traffic_batches (
  id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  boot_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  payload_hash TEXT NOT NULL,
  collected_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  UNIQUE(node_id, boot_id, sequence),
  FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);`
	if _, err := db.Exec(legacyDDL); err != nil {
		t.Fatalf("seed legacy schema: %v", err)
	}

	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// The nodes table must be gone after migration.
	var nodesCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='nodes'`).Scan(&nodesCount); err != nil {
		t.Fatalf("query nodes table: %v", err)
	}
	if nodesCount != 0 {
		t.Fatalf("nodes table still present after migration")
	}

	// And traffic_batches must no longer carry the legacy FK.
	var ddl string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='traffic_batches'`).Scan(&ddl); err != nil {
		t.Fatalf("query traffic_batches ddl: %v", err)
	}
	if strings.Contains(ddl, "REFERENCES nodes") {
		t.Fatalf("traffic_batches still references nodes: %s", ddl)
	}
}

func TestMigrateRepairsMissingSingBoxSecretColumnAfterVersionRecorded(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE singbox_config RENAME TO singbox_config_current; CREATE TABLE singbox_config (id INTEGER PRIMARY KEY CHECK (id = 1),log_level TEXT NOT NULL DEFAULT 'info',log_timestamp INTEGER NOT NULL DEFAULT 1,dns_servers_json TEXT NOT NULL DEFAULT '[]',dns_final TEXT NOT NULL DEFAULT '',clash_api_enabled INTEGER NOT NULL DEFAULT 1,clash_api_listen TEXT NOT NULL DEFAULT '127.0.0.1:9090',v2ray_api_enabled INTEGER NOT NULL DEFAULT 1,v2ray_api_listen TEXT NOT NULL DEFAULT '127.0.0.1:10000',default_outbound_tag TEXT NOT NULL DEFAULT 'direct',revision INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL); DROP TABLE singbox_config_current`); err != nil {
		t.Fatalf("seed incomplete sing-box schema: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("repair migration: %v", err)
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('singbox_config') WHERE name='clash_api_secret_ciphertext'`).Scan(&columnCount); err != nil {
		t.Fatalf("check secret column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("secret column count=%d, want 1", columnCount)
	}
}

func TestMigrateRebuildsPreLaunchTableShapes(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	// 模拟上线前的旧结构：runtime_artifacts 无 device_id、traffic_hourly 无
	// device_id、credentials 表仍在。再次迁移应整体 DROP 重建，不做行级迁移。
	if _, err := db.Exec(`DROP TABLE runtime_artifacts; CREATE TABLE runtime_artifacts (
  user_id TEXT NOT NULL,
  format TEXT NOT NULL,
  content BLOB NOT NULL,
  digest TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, format)
); DROP TABLE traffic_hourly; DROP TABLE traffic_daily; DROP TABLE traffic_cursors;
CREATE TABLE traffic_hourly (
  bucket_start INTEGER NOT NULL, node_id TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '', protocol_id TEXT NOT NULL DEFAULT '',
  up_bytes INTEGER NOT NULL DEFAULT 0, down_bytes INTEGER NOT NULL DEFAULT 0,
  sample_count INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL,
  PRIMARY KEY(bucket_start,node_id,user_id,protocol_id)
); CREATE TABLE credentials (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, kind TEXT NOT NULL)`); err != nil {
		t.Fatalf("seed legacy shapes: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("upgrade migration: %v", err)
	}
	for table, column := range map[string]string{"runtime_artifacts": "device_id", "traffic_hourly": "device_id", "traffic_daily": "device_id", "traffic_cursors": "device_id"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&count); err != nil {
			t.Fatalf("check %s.%s: %v", table, column, err)
		}
		if count != 1 {
			t.Fatalf("%s.%s column count=%d, want 1", table, column, count)
		}
	}
	var credentials int
	if err := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='credentials'`).Scan(&credentials); err != nil {
		t.Fatalf("check credentials table: %v", err)
	}
	if credentials != 0 {
		t.Fatal("legacy credentials table must be dropped")
	}
}

func TestMigrateRepeatedDoesNotDestroyTrafficData(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"traffic_hourly", "traffic_daily"} {
		if _, err := db.Exec(`INSERT INTO `+table+`(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at) VALUES(?,?,?,?,?,1,1,1,?)`, 10000000, "local", "user-1", "", "device-1", 10000000); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	for _, table := range []string{"traffic_hourly", "traffic_daily"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s rows=%d after re-migrate, want 1", table, count)
		}
	}
}

func TestMigrateKeepsSingleEffectiveSubscriptionPolicy(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	const legacySchema = `
DROP TABLE user_policies;
CREATE TABLE user_policies (user_id TEXT NOT NULL, policy_id TEXT NOT NULL, PRIMARY KEY(user_id,policy_id));
INSERT INTO users(id,name,enabled,quota_bytes,used_bytes,reset_interval_days,revision,created_at,updated_at) VALUES('user-1','User',1,0,0,0,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
INSERT INTO policies(id,name,description,enabled,default_action,revision,created_at,updated_at) VALUES('policy-a','Alpha','',1,'direct',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),('policy-z','Zulu','',1,'proxy',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
INSERT INTO user_policies VALUES ('user-1','policy-a'),('user-1','policy-z');`
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("seed legacy policies: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var policyID string
	if err := db.QueryRow(`SELECT policy_id FROM user_policies WHERE user_id='user-1'`).Scan(&policyID); err != nil {
		t.Fatalf("read migrated policy: %v", err)
	}
	if policyID != "policy-z" {
		t.Fatalf("policy=%q, want last effective policy-z", policyID)
	}
	if _, err := db.Exec(`INSERT INTO user_policies(user_id,policy_id) VALUES('user-1','policy-a')`); err == nil {
		t.Fatal("user accepted a second subscription policy")
	}
}

func TestMigrateAddsOutboundSettingsColumnAndKeepsRows(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	// Simulate a database created before the per-type settings blob existed by
	// rebuilding singbox_outbounds without settings_json, then re-migrate.
	if _, err := db.Exec(`DROP TABLE singbox_outbounds; CREATE TABLE singbox_outbounds (
  id TEXT PRIMARY KEY,
  tag TEXT NOT NULL COLLATE NOCASE UNIQUE,
  type TEXT NOT NULL,
  server TEXT NOT NULL DEFAULT '',
  server_port INTEGER NOT NULL DEFAULT 0,
  username TEXT NOT NULL DEFAULT '',
  password_ciphertext BLOB,
  revision INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)`); err != nil {
		t.Fatalf("seed legacy outbound schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO singbox_outbounds(id,tag,type,server,server_port,revision,created_at,updated_at) VALUES('out-1','legacy-socks','socks','127.0.0.1',1080,1,1,1)`); err != nil {
		t.Fatalf("seed legacy outbound row: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("upgrade migration: %v", err)
	}
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('singbox_outbounds') WHERE name='settings_json'`).Scan(&columnCount); err != nil {
		t.Fatalf("check settings column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("settings_json column count=%d, want 1", columnCount)
	}
	var tag string
	if err := db.QueryRow(`SELECT tag FROM singbox_outbounds WHERE id='out-1'`).Scan(&tag); err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
	if tag != "legacy-socks" {
		t.Fatalf("legacy row tag=%q", tag)
	}
}

func TestMigrateTopsUpAIRuleSetsOnce(t *testing.T) {
	db, err := Open(Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// 模拟 gemini/anthropic 出现前的老库：删掉两个 AI 规则集与升级 marker。
	if _, err := db.Exec(`DELETE FROM singbox_rule_sets WHERE tag IN ('gemini','anthropic'); DELETE FROM seed_markers WHERE name='seed-rulesets-ai-1'`); err != nil {
		t.Fatalf("reset to legacy state: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("upgrade Migrate: %v", err)
	}
	for _, tag := range []string{"gemini", "anthropic"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(1) FROM singbox_rule_sets WHERE tag=?`, tag).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tag, err)
		}
		if count != 1 {
			t.Fatalf("tag %s count=%d, want 1 after top-up", tag, count)
		}
	}
	// marker 已记录：再次删除后不得复活。
	if _, err := db.Exec(`DELETE FROM singbox_rule_sets WHERE tag='gemini'`); err != nil {
		t.Fatalf("delete gemini: %v", err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatalf("third Migrate: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM singbox_rule_sets WHERE tag='gemini'`).Scan(&count); err != nil {
		t.Fatalf("count gemini: %v", err)
	}
	if count != 0 {
		t.Fatalf("deleted gemini rule set was re-seeded after marker recorded")
	}
}
