package argo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

const tokenSecretKey = "argo-tunnel-token"

const tunnelColumns = `id,name,enabled,mode,fixed_domain,preferred_address,protocol_id,token_ciphertext,assigned_domain,status,last_error,started_at,revision,created_at,updated_at`

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

// List returns tunnels for the API with tokens masked.
func (s *Store) List(ctx context.Context) ([]Tunnel, error) {
	tunnels, err := s.ListRuntime(ctx)
	if err != nil {
		return nil, err
	}
	for index := range tunnels {
		tunnels[index] = maskToken(tunnels[index])
	}
	return tunnels, nil
}

// ListRuntime returns tunnels with decrypted tokens for the supervisor.
// Tokens are materialized only after the result set is closed (SQLite runs
// with a single connection).
func (s *Store) ListRuntime(ctx context.Context) ([]Tunnel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tunnelColumns+` FROM argo_tunnels ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	type rawTunnel struct {
		value Tunnel
		token []byte
	}
	raw := []rawTunnel{}
	for rows.Next() {
		value, token, err := scanTunnel(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		raw = append(raw, rawTunnel{value: value, token: token})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]Tunnel, 0, len(raw))
	for _, item := range raw {
		value := item.value
		if len(item.token) > 0 {
			plain, err := s.secrets.Decrypt(ctx, tokenSecretKey, item.token)
			if err != nil {
				return nil, err
			}
			value.Token = string(plain)
		}
		values = append(values, value)
	}
	return values, nil
}

func (s *Store) Get(ctx context.Context, id string) (Tunnel, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+tunnelColumns+` FROM argo_tunnels WHERE id=?`, id)
	value, _, err := scanTunnel(row)
	if err != nil {
		return Tunnel{}, err
	}
	return maskToken(value), nil
}

func (s *Store) Create(ctx context.Context, value Tunnel, token *string) error {
	var encryptedToken []byte
	var err error
	if token != nil && *token != "" {
		encryptedToken, err = s.secrets.Encrypt(ctx, tokenSecretKey, []byte(*token))
		if err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO argo_tunnels(id,name,enabled,mode,fixed_domain,preferred_address,protocol_id,token_ciphertext,assigned_domain,status,last_error,started_at,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Name, boolInt(value.Enabled), value.Mode, value.FixedDomain, value.PreferredAddress, value.ProtocolID, encryptedToken, "", StatusStopped, "", nil, value.Revision, value.CreatedAt.Unix(), value.UpdatedAt.Unix())
	return common.MapSQLiteError(err)
}

func (s *Store) Update(ctx context.Context, value Tunnel, revision int64, token *string) error {
	// token == nil 表示沿用已存令牌（列不参与更新）；空串表示清除。
	query := `UPDATE argo_tunnels SET name=?,enabled=?,mode=?,fixed_domain=?,preferred_address=?,protocol_id=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`
	args := []any{value.Name, boolInt(value.Enabled), value.Mode, value.FixedDomain, value.PreferredAddress, value.ProtocolID, value.UpdatedAt.Unix(), value.ID, revision}
	if token != nil {
		var encryptedToken any
		if *token == "" {
			encryptedToken = nil
		} else {
			encrypted, err := s.secrets.Encrypt(ctx, tokenSecretKey, []byte(*token))
			if err != nil {
				return err
			}
			encryptedToken = encrypted
		}
		query = `UPDATE argo_tunnels SET name=?,enabled=?,mode=?,fixed_domain=?,preferred_address=?,protocol_id=?,token_ciphertext=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`
		args = []any{value.Name, boolInt(value.Enabled), value.Mode, value.FixedDomain, value.PreferredAddress, value.ProtocolID, encryptedToken, value.UpdatedAt.Unix(), value.ID, revision}
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrRevisionConflict
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM argo_tunnels WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

// UpdateRuntime persists supervisor state (assigned domain, status, last
// error) without touching the admin revision.
func (s *Store) UpdateRuntime(ctx context.Context, id string, update RuntimeUpdate, now time.Time) error {
	var startedAt any
	if update.StartedAt != nil {
		startedAt = update.StartedAt.Unix()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE argo_tunnels SET assigned_domain=?,status=?,last_error=?,started_at=?,updated_at=? WHERE id=?`,
		update.AssignedDomain, update.Status, update.LastError, startedAt, now.Unix(), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanTunnel(row scanner) (Tunnel, []byte, error) {
	var value Tunnel
	var enabled int
	var tokenCiphertext []byte
	var startedAt sql.NullInt64
	var createdAt, updatedAt common.Time
	err := row.Scan(&value.ID, &value.Name, &enabled, &value.Mode, &value.FixedDomain, &value.PreferredAddress, &value.ProtocolID, &tokenCiphertext, &value.AssignedDomain, &value.Status, &value.LastError, &startedAt, &value.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Tunnel{}, nil, common.ErrNotFound
	}
	if err != nil {
		return Tunnel{}, nil, err
	}
	value.Enabled = enabled != 0
	value.TokenSet = len(tokenCiphertext) > 0
	if startedAt.Valid {
		started := time.Unix(startedAt.Int64, 0).UTC()
		value.StartedAt = &started
	}
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, tokenCiphertext, nil
}

func maskToken(value Tunnel) Tunnel {
	value.Token = ""
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
