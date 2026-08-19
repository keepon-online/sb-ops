package protocol

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/keepon-online/sb-ops/internal/service/common"
	"github.com/keepon-online/sb-ops/internal/service/secret"
)

const settingsKeyName = "protocol-settings-aes-256"

type Store struct {
	db      *sql.DB
	secrets *secret.Store
}

func NewStore(db *sql.DB, secrets *secret.Store) *Store { return &Store{db: db, secrets: secrets} }

func (s *Store) Create(ctx context.Context, value Protocol) error {
	settings, err := s.encryptSettings(ctx, value.Settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO protocols(id,name,type,enabled,listen,listen_port,settings_json,certificate_id,revision,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Name, value.Type, value.Enabled, value.Listen, value.ListenPort,
		settings, optionalString(value.CertificateID), value.Revision, common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return mapError(err)
}

type protocolRow struct {
	value    Protocol
	settings string
}

// scanRow decodes one protocol row without touching the database, so a list
// query can iterate over open rows and defer settings decryption (which needs
// the single sqlite connection) until the rows are closed.
func scanRow(row common.Scanner) (protocolRow, error) {
	var value protocolRow
	var enabled int
	var createdAt, updatedAt common.Time
	var certificateID sql.NullString
	if err := row.Scan(&value.value.ID, &value.value.Name, &value.value.Type, &enabled, &value.value.Listen,
		&value.value.ListenPort, &value.settings, &certificateID, &value.value.Revision, &createdAt, &updatedAt); err != nil {
		return protocolRow{}, err
	}
	value.value.Enabled = enabled != 0
	if certificateID.Valid {
		value.value.CertificateID = &certificateID.String
	}
	value.value.CreatedAt, value.value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}

func (s *Store) decode(ctx context.Context, row protocolRow) (Protocol, error) {
	settings, err := s.decryptSettings(ctx, row.settings)
	if err != nil {
		return Protocol{}, err
	}
	row.value.Settings = settings
	return row.value, nil
}

func (s *Store) Get(ctx context.Context, id string) (Protocol, error) {
	row, err := scanRow(s.db.QueryRowContext(ctx, `SELECT id,name,type,enabled,listen,listen_port,settings_json,certificate_id,revision,created_at,updated_at FROM protocols WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Protocol{}, common.ErrNotFound
	}
	if err != nil {
		return Protocol{}, err
	}
	return s.decode(ctx, row)
}

func (s *Store) List(ctx context.Context) ([]Protocol, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,type,enabled,listen,listen_port,settings_json,certificate_id,revision,created_at,updated_at FROM protocols ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	var pending []protocolRow
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]Protocol, 0, len(pending))
	for _, row := range pending {
		value, err := s.decode(ctx, row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (s *Store) Update(ctx context.Context, value Protocol, expectedRevision int64) error {
	settings, err := s.encryptSettings(ctx, value.Settings)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE protocols SET name=?,type=?,enabled=?,listen=?,listen_port=?,settings_json=?,certificate_id=?,
  revision=revision+1,updated_at=? WHERE id=? AND revision=?`, value.Name, value.Type, value.Enabled,
		value.Listen, value.ListenPort, settings, optionalString(value.CertificateID), common.UnixTime(value.UpdatedAt), value.ID, expectedRevision)
	if err != nil {
		return mapError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM protocols WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

func (s *Store) CertificateExists(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM certificates WHERE id=?`, id).Scan(&count)
	return count > 0, err
}

func (s *Store) UsedListenPorts(ctx context.Context, transport string) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT listen_port
FROM protocols
WHERE enabled=1 AND CASE WHEN type IN ('hysteria2','tuic') THEN 'udp' ELSE 'tcp' END=?`, transport)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ports := map[int]bool{}
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			return nil, err
		}
		ports[port] = true
	}
	return ports, rows.Err()
}

func (s *Store) encryptSettings(ctx context.Context, settings map[string]any) (string, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	encrypted, err := s.secrets.Encrypt(ctx, settingsKeyName, raw)
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(encrypted), nil
}

func (s *Store) decryptSettings(ctx context.Context, encoded string) (map[string]any, error) {
	encrypted, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	raw, err := s.secrets.Decrypt(ctx, settingsKeyName, encrypted)
	if err != nil {
		return nil, err
	}
	settings := map[string]any{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func optionalString(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return strings.TrimSpace(*value)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "protocols.listen") || strings.Contains(lower, "idx_protocols_transport_port") {
		return &common.ValidationError{Fields: map[string]string{"listenPort": "is already in use on this listen address"}}
	}
	return common.MapSQLiteError(err)
}
