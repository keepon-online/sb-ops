package singbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Store struct {
	db      *sql.DB
	secrets interface {
		Encrypt(context.Context, string, []byte) ([]byte, error)
		Decrypt(context.Context, string, []byte) ([]byte, error)
	}
}

func NewStore(db *sql.DB, secrets interface {
	Encrypt(context.Context, string, []byte) ([]byte, error)
	Decrypt(context.Context, string, []byte) ([]byte, error)
}) *Store {
	return &Store{db: db, secrets: secrets}
}

func (s *Store) EnsureConfig(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO singbox_config(id,updated_at) VALUES(1,?)`, common.UnixTime(now))
	return err
}

func (s *Store) GetConfig(ctx context.Context) (Config, error) {
	var value Config
	var servers string
	var timestamp, clashEnabled, v2rayEnabled int
	var secret []byte
	var updatedAt common.Time
	err := s.db.QueryRowContext(ctx, `SELECT log_level,log_timestamp,dns_servers_json,dns_final,clash_api_enabled,clash_api_listen,clash_api_secret_ciphertext,v2ray_api_enabled,v2ray_api_listen,default_outbound_tag,revision,updated_at FROM singbox_config WHERE id=1`).Scan(
		&value.LogLevel, &timestamp, &servers, &value.DNSFinal, &clashEnabled, &value.ClashAPIListen, &secret, &v2rayEnabled, &value.V2RayAPIListen, &value.DefaultOutboundTag, &value.Revision, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, common.ErrNotFound
	}
	if err != nil {
		return Config{}, err
	}
	if err := json.Unmarshal([]byte(servers), &value.DNSServers); err != nil {
		return Config{}, err
	}
	value.LogTimestamp, value.ClashAPIEnabled, value.V2RayAPIEnabled = timestamp != 0, clashEnabled != 0, v2rayEnabled != 0
	value.ClashAPISecretSet = len(secret) > 0
	value.UpdatedAt = updatedAt.Time
	return value, nil
}

func (s *Store) UpdateConfig(ctx context.Context, value Config, revision int64, now time.Time, secretValue *string) error {
	servers, err := json.Marshal(value.DNSServers)
	if err != nil {
		return err
	}
	secret := any(nil)
	if secretValue != nil {
		if *secretValue != "" {
			secret, err = s.secrets.Encrypt(ctx, "singbox-clash-api-secret", []byte(*secretValue))
			if err != nil {
				return err
			}
		}
		result, err := s.db.ExecContext(ctx, `UPDATE singbox_config SET log_level=?,log_timestamp=?,dns_servers_json=?,dns_final=?,clash_api_enabled=?,clash_api_listen=?,clash_api_secret_ciphertext=?,v2ray_api_enabled=?,v2ray_api_listen=?,default_outbound_tag=?,revision=revision+1,updated_at=? WHERE id=1 AND revision=?`,
			value.LogLevel, boolInt(value.LogTimestamp), string(servers), value.DNSFinal, boolInt(value.ClashAPIEnabled), value.ClashAPIListen, secret, boolInt(value.V2RayAPIEnabled), value.V2RayAPIListen, value.DefaultOutboundTag, common.UnixTime(now), revision)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return common.ErrRevisionConflict
		}
		return nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE singbox_config SET log_level=?,log_timestamp=?,dns_servers_json=?,dns_final=?,clash_api_enabled=?,clash_api_listen=?,v2ray_api_enabled=?,v2ray_api_listen=?,default_outbound_tag=?,revision=revision+1,updated_at=? WHERE id=1 AND revision=?`,
		value.LogLevel, boolInt(value.LogTimestamp), string(servers), value.DNSFinal, boolInt(value.ClashAPIEnabled), value.ClashAPIListen, boolInt(value.V2RayAPIEnabled), value.V2RayAPIListen, value.DefaultOutboundTag, common.UnixTime(now), revision)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) ClashAPISecret(ctx context.Context) (string, error) {
	var encrypted []byte
	if err := s.db.QueryRowContext(ctx, `SELECT clash_api_secret_ciphertext FROM singbox_config WHERE id=1`).Scan(&encrypted); err != nil {
		return "", err
	}
	if len(encrypted) == 0 {
		return "", nil
	}
	plain, err := s.secrets.Decrypt(ctx, "singbox-clash-api-secret", encrypted)
	return string(plain), err
}

// SetClashAPISecret persists a Clash API secret only when no secret is present,
// so concurrent generation is safe: a lost race just leaves the other secret in
// place.
func (s *Store) SetClashAPISecret(ctx context.Context, secretValue string, now time.Time) error {
	encrypted, err := s.secrets.Encrypt(ctx, "singbox-clash-api-secret", []byte(secretValue))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE singbox_config
SET clash_api_secret_ciphertext=?,revision=revision+1,updated_at=?
WHERE id=1 AND (clash_api_secret_ciphertext IS NULL OR clash_api_secret_ciphertext=X'')`,
		encrypted, common.UnixTime(now))
	return err
}

// outboundSettingsSecretKey is the control-plane secret usage key for the
// encrypted per-type outbound settings blob (WARP wireguard parameters).
const outboundSettingsSecretKey = "singbox-outbound-settings"

const outboundColumns = `id,tag,type,server,server_port,username,password_ciphertext,settings_json,revision,created_at,updated_at`

func (s *Store) ListOutbounds(ctx context.Context) ([]Outbound, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+outboundColumns+` FROM singbox_outbounds ORDER BY tag COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Outbound{builtinOutbound("direct"), builtinOutbound("block")}
	scanned, err := scanOutboundRows(rows)
	if err != nil {
		return nil, err
	}
	for _, item := range scanned {
		if err := s.hydrateWarpSettings(ctx, &item.value, item.settings); err != nil {
			return nil, err
		}
		values = append(values, item.value)
	}
	return values, rows.Err()
}

func (s *Store) GetOutbound(ctx context.Context, id string) (Outbound, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+outboundColumns+` FROM singbox_outbounds WHERE id=?`, id)
	value, settings, _, err := scanOutboundRow(row)
	if err != nil {
		return Outbound{}, err
	}
	if err := s.hydrateWarpSettings(ctx, &value, settings); err != nil {
		return Outbound{}, err
	}
	return value, nil
}

// outboundRow is one materialized outbound row; ciphertexts are decrypted only
// after the result set is closed (SQLite runs with a single connection).
type outboundRow struct {
	value    Outbound
	settings []byte
	password []byte
}

func scanOutboundRows(rows *sql.Rows) ([]outboundRow, error) {
	values := []outboundRow{}
	for rows.Next() {
		value, settings, password, err := scanOutboundRow(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, outboundRow{value: value, settings: settings, password: password})
	}
	return values, rows.Err()
}

func scanOutboundRow(row scanner) (Outbound, []byte, []byte, error) {
	var value Outbound
	var settingsCiphertext, passwordCiphertext []byte
	var createdAt, updatedAt common.Time
	err := row.Scan(&value.ID, &value.Tag, &value.Type, &value.Server, &value.ServerPort, &value.Username, &passwordCiphertext, &settingsCiphertext, &value.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Outbound{}, nil, nil, common.ErrNotFound
	}
	if err != nil {
		return Outbound{}, nil, nil, err
	}
	value.HasPassword = len(passwordCiphertext) > 0
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, settingsCiphertext, passwordCiphertext, nil
}

func (s *Store) hydrateWarpSettings(ctx context.Context, value *Outbound, ciphertext []byte) error {
	if value.Type != OutboundTypeWarp || len(ciphertext) == 0 {
		return nil
	}
	plain, err := s.secrets.Decrypt(ctx, outboundSettingsSecretKey, ciphertext)
	if err != nil {
		return err
	}
	var settings WarpSettings
	if err := json.Unmarshal(plain, &settings); err != nil {
		return err
	}
	value.Warp = &settings
	return nil
}

// encryptWarpSettings marshals and encrypts the settings blob for storage.
// Returns nil for non-WARP outbounds.
func (s *Store) encryptWarpSettings(ctx context.Context, value Outbound) (any, error) {
	if value.Type != OutboundTypeWarp || value.Warp == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value.Warp)
	if err != nil {
		return nil, err
	}
	return s.secrets.Encrypt(ctx, outboundSettingsSecretKey, raw)
}

func (s *Store) CreateOutbound(ctx context.Context, value Outbound, password *string) error {
	var encrypted []byte
	var err error
	if password != nil && *password != "" {
		encrypted, err = s.secrets.Encrypt(ctx, "singbox-outbound-password", []byte(*password))
		if err != nil {
			return err
		}
	}
	settings, err := s.encryptWarpSettings(ctx, value)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO singbox_outbounds(id,tag,type,server,server_port,username,password_ciphertext,settings_json,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Tag, value.Type, value.Server, value.ServerPort, value.Username, encrypted, settings, value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return common.MapSQLiteError(err)
}

func (s *Store) UpdateOutbound(ctx context.Context, value Outbound, revision int64, password *string) error {
	var encrypted any
	if password != nil {
		if *password == "" {
			encrypted = nil
		} else {
			value, err := s.secrets.Encrypt(ctx, "singbox-outbound-password", []byte(*password))
			if err != nil {
				return err
			}
			encrypted = value
		}
	}
	settings, err := s.encryptWarpSettings(ctx, value)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE singbox_outbounds SET tag=?,server=?,server_port=?,username=?,password_ciphertext=?,settings_json=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		value.Tag, value.Server, value.ServerPort, value.Username, encrypted, settings, common.UnixTime(value.UpdatedAt), value.ID, revision)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

// CompilerOutbounds loads all outbounds with decrypted WARP settings (the
// compiler needs the plaintext private key) plus the SOCKS passwords by tag.
// Ciphertexts are decrypted only after the result set is closed because SQLite
// runs with a single connection.
func (s *Store) CompilerOutbounds(ctx context.Context) ([]Outbound, map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+outboundColumns+` FROM singbox_outbounds ORDER BY tag COLLATE NOCASE,id`)
	if err != nil {
		return nil, nil, err
	}
	scanned, err := scanOutboundRows(rows)
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, nil, err
	}
	values := make([]Outbound, 0, len(scanned))
	passwords := map[string]string{}
	for _, item := range scanned {
		if err := s.hydrateWarpSettings(ctx, &item.value, item.settings); err != nil {
			return nil, nil, err
		}
		values = append(values, item.value)
		if item.value.Type != OutboundTypeSOCKS || len(item.password) == 0 {
			continue
		}
		plain, err := s.secrets.Decrypt(ctx, "singbox-outbound-password", item.password)
		if err != nil {
			return nil, nil, err
		}
		passwords[item.value.Tag] = string(plain)
	}
	return values, passwords, nil
}

func (s *Store) DeleteOutbound(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM singbox_outbounds WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) OutboundReferenced(ctx context.Context, tag string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(1) FROM singbox_route_rules WHERE outbound_tag=?) + (SELECT COUNT(1) FROM singbox_config WHERE id=1 AND default_outbound_tag=?) + (SELECT COUNT(1) FROM singbox_rule_sets WHERE download_detour=?)`, tag, tag, tag).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Store) ListRules(ctx context.Context) ([]RouteRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,enabled,priority,conditions_json,outbound_tag,revision,created_at,updated_at FROM singbox_route_rules ORDER BY priority,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []RouteRule{}
	for rows.Next() {
		value, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) GetRule(ctx context.Context, id string) (RouteRule, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,enabled,priority,conditions_json,outbound_tag,revision,created_at,updated_at FROM singbox_route_rules WHERE id=?`, id)
	return scanRule(row)
}

func (s *Store) CreateRule(ctx context.Context, value RouteRule) error {
	conditions, err := json.Marshal(value.Conditions)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO singbox_route_rules(id,name,enabled,priority,conditions_json,outbound_tag,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Name, boolInt(value.Enabled), value.Priority, string(conditions), value.OutboundTag, value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return common.MapSQLiteError(err)
}

func (s *Store) UpdateRule(ctx context.Context, value RouteRule, revision int64) error {
	conditions, err := json.Marshal(value.Conditions)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE singbox_route_rules SET name=?,enabled=?,priority=?,conditions_json=?,outbound_tag=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		value.Name, boolInt(value.Enabled), value.Priority, string(conditions), value.OutboundTag, common.UnixTime(value.UpdatedAt), value.ID, revision)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) DeleteRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM singbox_route_rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) OrderRules(ctx context.Context, ids []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM singbox_route_rules`).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return &common.ValidationError{Fields: map[string]string{"ids": "must contain every route rule exactly once"}}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE singbox_route_rules SET priority=-priority-1`); err != nil {
		return err
	}
	seen := make(map[string]bool, len(ids))
	for index, id := range ids {
		if seen[id] {
			return &common.ValidationError{Fields: map[string]string{"ids": "contains duplicate rule IDs"}}
		}
		seen[id] = true
		result, err := tx.ExecContext(ctx, `UPDATE singbox_route_rules SET priority=?,revision=revision+1,updated_at=? WHERE id=?`, (index+1)*10, common.UnixTime(now), id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return common.ErrNotFound
		}
	}
	return tx.Commit()
}

func (s *Store) ListRuleSets(ctx context.Context) ([]RuleSet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,tag,format,url,download_detour,revision,created_at,updated_at FROM singbox_rule_sets ORDER BY tag COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []RuleSet{}
	for rows.Next() {
		value, err := scanRuleSet(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) GetRuleSet(ctx context.Context, id string) (RuleSet, error) {
	return scanRuleSet(s.db.QueryRowContext(ctx, `SELECT id,tag,format,url,download_detour,revision,created_at,updated_at FROM singbox_rule_sets WHERE id=?`, id))
}

func (s *Store) CreateRuleSet(ctx context.Context, value RuleSet) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO singbox_rule_sets(id,tag,format,url,download_detour,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		value.ID, value.Tag, value.Format, value.URL, value.DownloadDetour, value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return common.MapSQLiteError(err)
}

func (s *Store) UpdateRuleSet(ctx context.Context, value RuleSet, revision int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE singbox_rule_sets SET tag=?,format=?,url=?,download_detour=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		value.Tag, value.Format, value.URL, value.DownloadDetour, common.UnixTime(value.UpdatedAt), value.ID, revision)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) DeleteRuleSet(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM singbox_rule_sets WHERE id=?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) RuleSetReferenced(ctx context.Context, tag string) (bool, error) {
	rules, err := s.ListRules(ctx)
	if err != nil {
		return false, err
	}
	for _, rule := range rules {
		for _, ruleSetTag := range rule.Conditions.RuleSets {
			if ruleSetTag == tag {
				return true, nil
			}
		}
	}
	return false, nil
}

type scanner interface{ Scan(...any) error }

func scanRule(row scanner) (RouteRule, error) {
	var value RouteRule
	var enabled int
	var conditions string
	var createdAt, updatedAt common.Time
	err := row.Scan(&value.ID, &value.Name, &enabled, &value.Priority, &conditions, &value.OutboundTag, &value.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RouteRule{}, common.ErrNotFound
	}
	if err != nil {
		return RouteRule{}, err
	}
	if err := json.Unmarshal([]byte(conditions), &value.Conditions); err != nil {
		return RouteRule{}, err
	}
	value.Enabled = enabled != 0
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}

func builtinOutbound(tag string) Outbound { return Outbound{ID: tag, Tag: tag, Type: tag, Revision: 1} }
func scanRuleSet(row scanner) (RuleSet, error) {
	var value RuleSet
	var createdAt, updatedAt common.Time
	err := row.Scan(&value.ID, &value.Tag, &value.Format, &value.URL, &value.DownloadDetour, &value.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RuleSet{}, common.ErrNotFound
	}
	if err != nil {
		return RuleSet{}, err
	}
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
