package user

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

const credentialKeyName = "user-credential-aes-256"

type Store struct {
	db      *sql.DB
	secrets *secret.Store
}

// storedDevice carries a device row's encrypted credential material for writes.
type storedDevice struct {
	ID                 string
	UserID             string
	Name               string
	UAKey              string
	SourceIP           string
	Platform           string
	Client             string
	ClientVersion      string
	LastSeenAt         *time.Time
	TokenHash          []byte
	TokenCiphertext    []byte
	UUID               string
	PasswordCiphertext []byte
	RotatedAt          time.Time
	CreatedAt          time.Time
}

// ArtifactUser is the per-user compile input (availability fields only);
// credentials live on ArtifactDevice.
type ArtifactUser struct {
	ID         string
	Name       string
	Enabled    bool
	QuotaBytes int64
	UsedBytes  int64
	ExpiresAt  *time.Time
}

func (u ArtifactUser) AvailableAt(now time.Time) bool {
	return u.Enabled && (u.ExpiresAt == nil || u.ExpiresAt.After(now)) && (u.QuotaBytes == 0 || u.UsedBytes < u.QuotaBytes)
}

// ArtifactDevice is a device's compile input: DeviceID doubles as the inbound
// user name (the V2Ray stats attribution tag), UUID/Password are the device
// credential.
type ArtifactDevice struct {
	UserID   string
	DeviceID string
	Enabled  bool
	UUID     string
	Password string
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db, secrets: secret.NewStore(db)}
}

// userToken 是用户级订阅链接的签发/存储载体。
type userToken struct {
	Hash       []byte
	Ciphertext []byte
	Token      string
}

func (s *Store) Create(ctx context.Context, value User, token userToken) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO users (
  id,name,enabled,quota_bytes,used_bytes,expires_at,reset_interval_days,next_reset_at,
  revision,created_at,updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Name, value.Enabled, value.QuotaBytes, value.UsedBytes,
		common.TimeFromPtr(value.ExpiresAt), value.ResetIntervalDays, common.TimeFromPtr(value.NextResetAt),
		value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt),
	)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET subscription_token_hash=?,subscription_token_ciphertext=? WHERE id=?`,
		token.Hash, token.Ciphertext, value.ID); err != nil {
		return err
	}
	if err := replaceReferences(ctx, tx, value.ID, value.ProtocolIDs, value.PolicyIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) insertDevice(ctx context.Context, device storedDevice) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertDevice(ctx, tx, device); err != nil {
		return err
	}
	return tx.Commit()
}

func insertDevice(ctx context.Context, tx *sql.Tx, device storedDevice) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO devices (
		  id,user_id,name,enabled,used_bytes,ua_key,source_ip,platform,client,client_version,last_seen_at,token_hash,token_ciphertext,identity_uuid,password_ciphertext,rotated_at,created_at
		) VALUES (?,?,?,?,0,?,?,?,?,?,?,?,?,?,?,?,?)`,
		device.ID, device.UserID, device.Name, 1, device.UAKey, normalizeSourceIP(device.SourceIP), device.Platform, device.Client, device.ClientVersion, common.TimeFromPtr(device.LastSeenAt), device.TokenHash, device.TokenCiphertext,
		device.UUID, device.PasswordCiphertext, common.UnixTime(device.RotatedAt), common.UnixTime(device.CreatedAt))
	return common.MapSQLiteError(err)
}

const userSelect = `SELECT u.id,u.name,u.enabled,u.quota_bytes,u.used_bytes,u.expires_at,u.reset_interval_days,
       u.next_reset_at,u.revision,u.created_at,u.updated_at,
       (SELECT COUNT(1) FROM devices d WHERE d.user_id=u.id)
FROM users u`

func scanUser(row common.Scanner) (User, error) {
	var value User
	var enabled int
	var expiresAt, nextResetAt common.Time
	var createdAt, updatedAt common.Time
	if err := row.Scan(&value.ID, &value.Name, &enabled, &value.QuotaBytes, &value.UsedBytes, &expiresAt,
		&value.ResetIntervalDays, &nextResetAt, &value.Revision, &createdAt, &updatedAt, &value.DeviceCount); err != nil {
		return User{}, err
	}
	value.Enabled = enabled != 0
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	value.ExpiresAt = expiresAt.Ptr()
	value.NextResetAt = nextResetAt.Ptr()
	return value, nil
}

func (s *Store) Get(ctx context.Context, id string) (User, error) {
	value, err := scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE u.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, common.ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if value.ProtocolIDs, err = s.referenceIDs(ctx, "user_protocols", "protocol_id", id); err != nil {
		return User{}, err
	}
	if value.PolicyIDs, err = s.referenceIDs(ctx, "user_policies", "policy_id", id); err != nil {
		return User{}, err
	}
	value.Status = value.StatusAt(time.Now().UTC())
	return value, nil
}

func (s *Store) List(ctx context.Context, input ListInput) (Page, error) {
	offset, _ := strconv.Atoi(input.Cursor)
	if offset < 0 {
		offset = 0
	}
	limit := input.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	where := []string{"1=1"}
	args := make([]any, 0, 5)
	if query := strings.TrimSpace(input.Query); query != "" {
		where = append(where, "u.name LIKE ? ESCAPE '\\'")
		query = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query)
		args = append(args, "%"+query+"%")
	}
	now := common.UnixTime(time.Now())
	switch input.Status {
	case StatusActive:
		where = append(where, "u.enabled=1 AND (u.expires_at IS NULL OR u.expires_at>?) AND (u.quota_bytes=0 OR u.used_bytes<u.quota_bytes)")
		args = append(args, now)
	case StatusDisabled:
		where = append(where, "u.enabled=0")
	case StatusExpired:
		where = append(where, "u.enabled=1 AND u.expires_at IS NOT NULL AND u.expires_at<=?")
		args = append(args, now)
	case StatusQuotaExceeded:
		where = append(where, "u.enabled=1 AND (u.expires_at IS NULL OR u.expires_at>?) AND u.quota_bytes>0 AND u.used_bytes>=u.quota_bytes")
		args = append(args, now)
	}
	order := "u.created_at DESC,u.id DESC"
	switch input.Sort {
	case "name", "name:asc":
		order = "u.name COLLATE NOCASE ASC,u.id ASC"
	case "name:desc":
		order = "u.name COLLATE NOCASE DESC,u.id DESC"
	case "usedBytes", "usedBytes:desc":
		order = "u.used_bytes DESC,u.id DESC"
	case "usedBytes:asc":
		order = "u.used_bytes ASC,u.id ASC"
	case "createdAt:asc":
		order = "u.created_at ASC,u.id ASC"
	}
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryContext(ctx, userSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return Page{}, err
	}
	var values []User
	for rows.Next() {
		value, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return Page{}, err
		}
		values = append(values, value)
	}
	if err := rows.Close(); err != nil {
		return Page{}, err
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Items: make([]User, 0, min(len(values), limit))}
	if len(values) > 0 {
		kept := values[:min(len(values), limit)]
		ids := make([]string, len(kept))
		for i := range kept {
			ids[i] = kept[i].ID
		}
		protocols, err := s.referenceIDMap(ctx, "user_protocols", "protocol_id", ids)
		if err != nil {
			return Page{}, err
		}
		policies, err := s.referenceIDMap(ctx, "user_policies", "policy_id", ids)
		if err != nil {
			return Page{}, err
		}
		now := time.Now().UTC()
		for _, value := range kept {
			value.ProtocolIDs = protocols[value.ID]
			value.PolicyIDs = policies[value.ID]
			value.Status = value.StatusAt(now)
			page.Items = append(page.Items, value)
		}
	}
	if len(values) > limit {
		page.NextCursor = strconv.Itoa(offset + limit)
	}
	return page, nil
}

func (s *Store) ListArtifactUsers(ctx context.Context) ([]ArtifactUser, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT u.id,u.name,u.enabled,u.quota_bytes,u.used_bytes,u.expires_at
FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	values := []ArtifactUser{}
	for rows.Next() {
		var item ArtifactUser
		var enabled int
		var expiresAt common.Time
		if err := rows.Scan(&item.ID, &item.Name, &enabled, &item.QuotaBytes, &item.UsedBytes, &expiresAt); err != nil {
			rows.Close()
			return nil, err
		}
		item.Enabled = enabled != 0
		item.ExpiresAt = expiresAt.Ptr()
		values = append(values, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	return values, rows.Close()
}

// ListArtifactDevices returns every device with decrypted credentials for the
// compiler (one inbound user entry per device).
func (s *Store) ListArtifactDevices(ctx context.Context) ([]ArtifactDevice, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT d.user_id,d.id,d.enabled,d.identity_uuid,d.password_ciphertext
FROM devices d ORDER BY d.user_id,d.id`)
	if err != nil {
		return nil, err
	}
	type storedDeviceCredential struct {
		device    ArtifactDevice
		encrypted []byte
	}
	stored := []storedDeviceCredential{}
	for rows.Next() {
		var item storedDeviceCredential
		var enabled int
		if err := rows.Scan(&item.device.UserID, &item.device.DeviceID, &enabled, &item.device.UUID, &item.encrypted); err != nil {
			rows.Close()
			return nil, err
		}
		item.device.Enabled = enabled != 0
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	values := make([]ArtifactDevice, 0, len(stored))
	for _, item := range stored {
		password, err := s.secrets.Decrypt(ctx, credentialKeyName, item.encrypted)
		if err != nil {
			return nil, err
		}
		item.device.Password = string(password)
		values = append(values, item.device)
	}
	return values, nil
}

// DeviceUserMap loads the deviceID→userID attribution map for the traffic
// collector (V2Ray stats tags are device IDs).
func (s *Store) DeviceUserMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id FROM devices`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var deviceID, userID string
		if err := rows.Scan(&deviceID, &userID); err != nil {
			return nil, err
		}
		result[deviceID] = userID
	}
	return result, rows.Err()
}

func (s *Store) Update(ctx context.Context, value User, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE users SET name=?,enabled=?,quota_bytes=?,expires_at=?,reset_interval_days=?,next_reset_at=?,
  revision=revision+1,updated_at=? WHERE id=? AND revision=?`,
		value.Name, value.Enabled, value.QuotaBytes, common.TimeFromPtr(value.ExpiresAt),
		value.ResetIntervalDays, common.TimeFromPtr(value.NextResetAt), common.UnixTime(value.UpdatedAt),
		value.ID, expectedRevision,
	)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return common.ErrRevisionConflict
	}
	if err := replaceReferences(ctx, tx, value.ID, value.ProtocolIDs, value.PolicyIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) SetStatus(ctx context.Context, ids []string, enabled bool, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE users SET enabled=?,revision=revision+1,updated_at=? WHERE id=?`, enabled, common.UnixTime(now), id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return common.ErrNotFound
		}
	}
	return tx.Commit()
}

// IssueUserToken signs a fresh user-level subscription token (does not persist).
func (s *Store) IssueUserToken(ctx context.Context, userID string, now time.Time) (userToken, error) {
	token, err := randomHex(24)
	if err != nil {
		return userToken{}, err
	}
	ciphertext, err := s.EncryptSecret(ctx, token)
	if err != nil {
		return userToken{}, fmt.Errorf("加密用户订阅令牌: %w", err)
	}
	return userToken{Hash: HashToken(token), Ciphertext: ciphertext, Token: token}, nil
}

// UserToken returns the user-level subscription token, lazily issuing one for
// users created before the column existed.
func (s *Store) UserToken(ctx context.Context, userID string) (string, error) {
	if err := s.ensureUser(ctx, userID); err != nil {
		return "", err
	}
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `SELECT subscription_token_ciphertext FROM users WHERE id=?`, userID).Scan(&ciphertext)
	if err != nil {
		return "", err
	}
	if len(ciphertext) == 0 {
		token, err := s.IssueUserToken(ctx, userID, time.Now().UTC())
		if err != nil {
			return "", err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET subscription_token_hash=?,subscription_token_ciphertext=? WHERE id=?`, token.Hash, token.Ciphertext, userID); err != nil {
			return "", err
		}
		return token.Token, nil
	}
	plaintext, err := s.secrets.Decrypt(ctx, credentialKeyName, ciphertext)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// RotateUserToken re-issues the user-level subscription link; the old link dies.
func (s *Store) RotateUserToken(ctx context.Context, userID string) (string, error) {
	if err := s.ensureUser(ctx, userID); err != nil {
		return "", err
	}
	token, err := s.IssueUserToken(ctx, userID, time.Now().UTC())
	if err != nil {
		return "", err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE users SET subscription_token_hash=?,subscription_token_ciphertext=? WHERE id=?`, token.Hash, token.Ciphertext, userID)
	if err != nil {
		return "", err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return "", common.ErrNotFound
	}
	return token.Token, nil
}

// ListDevices returns a user's devices with decrypted subscription tokens so
// the panel can render copyable links directly.
func (s *Store) ListDevices(ctx context.Context, userID string) ([]Device, error) {
	if err := s.ensureUser(ctx, userID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT d.id,d.name,d.enabled,d.used_bytes,d.token_ciphertext,d.identity_uuid,d.rotated_at,d.created_at,d.ua_key,
       d.source_ip,d.platform,d.client,d.client_version,d.last_seen_at
FROM devices d WHERE d.user_id=? ORDER BY d.created_at,d.id`, userID)
	if err != nil {
		return nil, err
	}
	type storedListDevice struct {
		device   Device
		enabled  int
		token    []byte
		uaKey    string
		sourceIP string
		lastSeen common.Time
		rotated  common.Time
		created  common.Time
	}
	stored := []storedListDevice{}
	for rows.Next() {
		var item storedListDevice
		if err := rows.Scan(&item.device.ID, &item.device.Name, &item.enabled, &item.device.UsedBytes, &item.token, &item.device.UUID, &item.rotated, &item.created, &item.uaKey, &item.sourceIP, &item.device.Platform, &item.device.Client, &item.device.ClientVersion, &item.lastSeen); err != nil {
			rows.Close()
			return nil, err
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	values := make([]Device, 0, len(stored))
	for _, item := range stored {
		item.device.Enabled = item.enabled != 0
		item.device.Auto = item.uaKey != ""
		item.device.SourceIP = item.sourceIP
		item.device.RotatedAt, item.device.CreatedAt = item.rotated.Time, item.created.Time
		if !item.lastSeen.IsZero() {
			lastSeen := item.lastSeen.Time
			item.device.LastSeenAt = &lastSeen
		}
		if len(item.token) > 0 {
			token, err := s.secrets.Decrypt(ctx, credentialKeyName, item.token)
			if err != nil {
				return nil, err
			}
			item.device.Token = string(token)
		}
		values = append(values, item.device)
	}
	return values, nil
}

// UpdateDeviceMetadata records the latest observed client information. The update
// is throttled to avoid turning frequent subscription refreshes into write load.
func (s *Store) UpdateDeviceMetadata(ctx context.Context, userID, deviceID string, metadata DeviceMetadata, minInterval time.Duration) error {
	platform := strings.TrimSpace(metadata.Platform)
	if platform == "" {
		platform = "unknown"
	}
	client := strings.TrimSpace(metadata.Client)
	version := strings.TrimSpace(metadata.ClientVersion)
	sourceIP := normalizeSourceIP(metadata.SourceIP)
	seenAt := metadata.LastSeenAt
	if seenAt == nil {
		now := time.Now().UTC()
		seenAt = &now
	}
	cutoff := seenAt.Add(-minInterval).Unix()
	// 来源地址为空（自环拉取被订阅服务丢弃、或 UA 未携带）时保留已存地址，
	// 只刷新其余元数据；非空且不同才覆盖。
	_, err := s.db.ExecContext(ctx, `
UPDATE devices
SET source_ip=COALESCE(NULLIF(?,''),source_ip),platform=?,client=?,client_version=?,last_seen_at=?
WHERE id=? AND user_id=? AND (source_ip<>? OR last_seen_at IS NULL OR last_seen_at <= ?)`,
		sourceIP, platform, client, version, common.TimeFromPtr(seenAt), deviceID, userID, sourceIP, cutoff)
	return err
}

// RenameDevice best-effort renames an auto-discovered device after its source
// IP changed. A name collision under UNIQUE(user_id, name) surfaces as an
// error the caller may ignore; the metadata update still applies.
func (s *Store) RenameDevice(ctx context.Context, userID, deviceID, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devices SET name=? WHERE id=? AND user_id=?`, name, deviceID, userID)
	return err
}

// UnknownPlatformDeviceRef identifies a device whose platform has not been
// determined yet, together with the source address its subscription was last
// fetched from.
type UnknownPlatformDeviceRef struct {
	UserID   string
	DeviceID string
	SourceIP string
}

// UnknownPlatformDevices lists devices still on the unknown platform that have
// an observed source address, keyed for traffic-portrait attribution.
func (s *Store) UnknownPlatformDevices(ctx context.Context) ([]UnknownPlatformDeviceRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,id,source_ip FROM devices WHERE platform='unknown' AND source_ip<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []UnknownPlatformDeviceRef{}
	for rows.Next() {
		var item UnknownPlatformDeviceRef
		if err := rows.Scan(&item.UserID, &item.DeviceID, &item.SourceIP); err != nil {
			return nil, err
		}
		values = append(values, item)
	}
	return values, rows.Err()
}

// UpdateDevicePlatform fills in an inferred platform. The guard on
// platform='unknown' keeps an already-identified value (from the subscription
// User-Agent) authoritative; inference never overrides it.
func (s *Store) UpdateDevicePlatform(ctx context.Context, userID, deviceID, platform string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devices SET platform=? WHERE id=? AND user_id=? AND platform='unknown'`, platform, deviceID, userID)
	return err
}

func normalizeSourceIP(value string) string {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if zone := strings.LastIndexByte(value, '%'); zone >= 0 {
		value = value[:zone]
	}
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return ""
}

// CountDevices returns how many devices a user already has.
func (s *Store) CountDevices(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM devices WHERE user_id=?`, userID).Scan(&count)
	return count, err
}

// FindDeviceByUAKey locates the auto-discovered device owning a UA + source IP
// fingerprint. Only id/name/enabled are populated; callers that need
// credentials use ListDevices. An empty source IP matches only legacy rows with
// an empty source IP.
func (s *Store) FindDeviceByUAKey(ctx context.Context, userID, uaKey, sourceIP string) (Device, bool, error) {
	var device Device
	var enabled int
	sourceIP = normalizeSourceIP(sourceIP)
	err := s.db.QueryRowContext(ctx, `SELECT id,name,enabled FROM devices WHERE user_id=? AND ua_key=? AND source_ip=?`, userID, uaKey, sourceIP).Scan(&device.ID, &device.Name, &enabled)
	if errors.Is(err, sql.ErrNoRows) && sourceIP != "" {
		// Upgrade one pre-IP automatic device in place instead of creating an
		// orphan duplicate on the first request after the migration.
		err = s.db.QueryRowContext(ctx, `SELECT id,name,enabled FROM devices WHERE user_id=? AND ua_key=? AND source_ip='' ORDER BY created_at,id LIMIT 1`, userID, uaKey).Scan(&device.ID, &device.Name, &enabled)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// 该客户端名下仅一台设备时视为同一台：IP 变化（换网络/换出口）与
		// 来源不可观测（经代理自环回面板的拉取）都原地更新而不是留下永不
		// 更新的孤儿设备；已有多台（同客户端多出口）无法判断是哪台，保持
		// not found 由调用方新建或回退。
		var count int
		if countErr := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM devices WHERE user_id=? AND ua_key=?`, userID, uaKey).Scan(&count); countErr == nil && count == 1 {
			err = s.db.QueryRowContext(ctx, `SELECT id,name,enabled FROM devices WHERE user_id=? AND ua_key=?`, userID, uaKey).Scan(&device.ID, &device.Name, &enabled)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, err
	}
	device.Enabled = enabled != 0
	device.Auto = true
	return device, true, nil
}

// RotateDevice replaces one device's credential material in place.
func (s *Store) RotateDevice(ctx context.Context, userID, deviceID string, device storedDevice) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE devices SET token_hash=?,token_ciphertext=?,identity_uuid=?,password_ciphertext=?,rotated_at=?
WHERE id=? AND user_id=?`, device.TokenHash, device.TokenCiphertext, device.UUID, device.PasswordCiphertext,
		common.UnixTime(device.RotatedAt), deviceID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

// SetDeviceEnabled toggles one device.
func (s *Store) SetDeviceEnabled(ctx context.Context, userID, deviceID string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE devices SET enabled=? WHERE id=? AND user_id=?`, enabled, deviceID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

// DeleteDevice removes one device; its subscription token dies with the row.
func (s *Store) DeleteDevice(ctx context.Context, userID, deviceID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id=? AND user_id=?`, deviceID, userID)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) ensureUser(ctx context.Context, userID string) error {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE id=?`, userID).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) ResetTraffic(ctx context.Context, id string, now time.Time) error {
	return s.setTraffic(ctx, id, "reset", 0, now)
}

func (s *Store) AdjustTraffic(ctx context.Context, id, mode string, bytes int64, now time.Time) error {
	return s.setTraffic(ctx, id, mode, bytes, now)
}

// AdjustQuota adds to or sets the traffic quota. A zero quota means unlimited;
// the add form refuses to drop the quota below zero.
func (s *Store) AdjustQuota(ctx context.Context, id, mode string, bytes int64, now time.Time) error {
	var query string
	switch mode {
	case "set":
		query = `UPDATE users SET quota_bytes=?,revision=revision+1,updated_at=? WHERE id=? AND ?>=0`
	case "add":
		query = `UPDATE users SET quota_bytes=quota_bytes+?,revision=revision+1,updated_at=? WHERE id=? AND quota_bytes+?>=0`
	default:
		return &common.ValidationError{Fields: map[string]string{"mode": "must be add or set"}}
	}
	result, err := s.db.ExecContext(ctx, query, bytes, common.UnixTime(now), id, bytes)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return getErr
		}
		return &common.ValidationError{Fields: map[string]string{"bytes": "quota must not be negative"}}
	}
	return nil
}

// setTraffic mutates the quota counters only: users/devices used_bytes are
// zeroed or adjusted in place while traffic_hourly history is left untouched.
// Window statistics (overview 24h totals) therefore keep counting pre-reset
// traffic — the counter and the history are meant to diverge.
func (s *Store) setTraffic(ctx context.Context, id, mode string, bytes int64, now time.Time) error {
	switch mode {
	case "reset":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err = tx.ExecContext(ctx, `UPDATE devices SET used_bytes=0 WHERE user_id=?`, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET used_bytes=0,revision=revision+1,updated_at=? WHERE id=?`, common.UnixTime(now), id)
		if err != nil {
			return err
		}
		return tx.Commit()
	case "set":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		result, err := tx.ExecContext(ctx, `UPDATE users SET used_bytes=?,revision=revision+1,updated_at=? WHERE id=? AND ?>=0`, bytes, common.UnixTime(now), id, bytes)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return &common.ValidationError{Fields: map[string]string{"bytes": "used traffic must not be negative"}}
		}
		if err := distributeDeviceTraffic(ctx, tx, id, bytes, now); err != nil {
			return err
		}
		return tx.Commit()
	case "add":
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		result, err := tx.ExecContext(ctx, `UPDATE users SET used_bytes=used_bytes+?,revision=revision+1,updated_at=? WHERE id=? AND used_bytes+?>=0`, bytes, common.UnixTime(now), id, bytes)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			if _, getErr := s.Get(ctx, id); getErr != nil {
				return getErr
			}
			return &common.ValidationError{Fields: map[string]string{"bytes": "used traffic must not be negative"}}
		}
		if err := distributeDeviceTrafficByDelta(ctx, tx, id, bytes, now); err != nil {
			return err
		}
		return tx.Commit()
	default:
		return &common.ValidationError{Fields: map[string]string{"mode": "must be add or set"}}
	}
}

// distributeDeviceTraffic 按设备当前用量占比分摊新的用户级总量。
func distributeDeviceTraffic(ctx context.Context, tx *sql.Tx, userID string, totalBytes int64, now time.Time) error {
	type deviceUsage struct {
		id   string
		used int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,used_bytes FROM devices WHERE user_id=?`, userID)
	if err != nil {
		return err
	}
	var devices []deviceUsage
	for rows.Next() {
		var d deviceUsage
		if err := rows.Scan(&d.id, &d.used); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, d)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	var currentTotal int64
	for _, d := range devices {
		currentTotal += d.used
	}
	for _, d := range devices {
		share := int64(0)
		if currentTotal > 0 {
			share = totalBytes * d.used / currentTotal
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET used_bytes=? WHERE id=?`, share, d.id); err != nil {
			return err
		}
	}
	return nil
}

// distributeDeviceTrafficByDelta 按设备当前用量占比分摊增量值。
func distributeDeviceTrafficByDelta(ctx context.Context, tx *sql.Tx, userID string, delta int64, now time.Time) error {
	type deviceUsage struct {
		id   string
		used int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,used_bytes FROM devices WHERE user_id=?`, userID)
	if err != nil {
		return err
	}
	var devices []deviceUsage
	for rows.Next() {
		var d deviceUsage
		if err := rows.Scan(&d.id, &d.used); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, d)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(devices) == 0 {
		return nil
	}
	var currentTotal int64
	for _, d := range devices {
		currentTotal += d.used
	}
	for _, d := range devices {
		deviceDelta := delta
		if currentTotal > 0 {
			deviceDelta = delta * d.used / currentTotal
		}
		if deviceDelta == 0 {
			deviceDelta = delta
		}
		newUsed := d.used + deviceDelta
		if newUsed < 0 {
			newUsed = 0
		}
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET used_bytes=? WHERE id=?`, newUsed, d.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ValidateReferences(ctx context.Context, protocolIDs, policyIDs []string) error {
	fields := map[string]string{}
	for _, id := range protocolIDs {
		if ok, err := s.exists(ctx, "protocols", id); err != nil {
			return err
		} else if !ok {
			fields["protocolIds"] = "contains an unknown protocol"
			break
		}
	}
	for _, id := range policyIDs {
		if ok, err := s.exists(ctx, "policies", id); err != nil {
			return err
		} else if !ok {
			fields["policyIds"] = "contains an unknown policy"
			break
		}
	}
	if len(fields) > 0 {
		return &common.ValidationError{Fields: fields}
	}
	return nil
}

func (s *Store) EncryptSecret(ctx context.Context, plaintext string) ([]byte, error) {
	return s.secrets.Encrypt(ctx, credentialKeyName, []byte(plaintext))
}

func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func (s *Store) referenceIDs(ctx context.Context, table, column, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE user_id=? ORDER BY `+column, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// referenceIDMap loads the reference columns for many users in one query so
// List does not issue one query per user.
func (s *Store) referenceIDMap(ctx context.Context, table, column string, userIDs []string) (map[string][]string, error) {
	result := map[string][]string{}
	if len(userIDs) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(userIDs)), ",")
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,`+column+` FROM `+table+` WHERE user_id IN (`+placeholders+`) ORDER BY user_id,`+column, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, id string
		if err := rows.Scan(&userID, &id); err != nil {
			return nil, err
		}
		result[userID] = append(result[userID], id)
	}
	return result, rows.Err()
}

func (s *Store) exists(ctx context.Context, table, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table+` WHERE id=?`, id).Scan(&count)
	return count > 0, err
}

func replaceReferences(ctx context.Context, tx *sql.Tx, userID string, protocolIDs, policyIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_protocols WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_policies WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, id := range uniqueStrings(protocolIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_protocols(user_id,protocol_id) VALUES(?,?)`, userID, id); err != nil {
			return err
		}
	}
	for _, id := range uniqueStrings(policyIDs) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_policies(user_id,policy_id) VALUES(?,?)`, userID, id); err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func newStoredDevice(ctx context.Context, store *Store, deviceID, userID, name, uaKey, token, identityUUID, password string, now time.Time) (storedDevice, error) {
	tokenCiphertext, err := store.EncryptSecret(ctx, token)
	if err != nil {
		return storedDevice{}, fmt.Errorf("加密设备令牌: %w", err)
	}
	passwordCiphertext, err := store.EncryptSecret(ctx, password)
	if err != nil {
		return storedDevice{}, fmt.Errorf("加密设备凭据: %w", err)
	}
	return storedDevice{
		ID: deviceID, UserID: userID, Name: name, UAKey: uaKey, Platform: "unknown",
		TokenHash: HashToken(token), TokenCiphertext: tokenCiphertext,
		UUID: identityUUID, PasswordCiphertext: passwordCiphertext,
		RotatedAt: now, CreatedAt: now,
	}, nil
}
