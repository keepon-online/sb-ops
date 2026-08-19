package certificate

import (
	"context"
	"database/sql"
	"errors"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type record struct {
	Certificate
	CertificatePEM      string
	PrivateKeyEncrypted []byte
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Create(ctx context.Context, value record) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO certificates (
  id,name,source,domain,certificate_pem,private_key_ciphertext,fingerprint,not_before,not_after,revision,created_at,updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.Name, value.Source, value.Domain, value.CertificatePEM, value.PrivateKeyEncrypted,
		value.Fingerprint, common.UnixTime(value.NotBefore), common.UnixTime(value.NotAfter), value.Revision,
		common.UnixTime(value.CreatedAt), common.UnixTime(value.UpdatedAt))
	return common.MapSQLiteError(err)
}

const certificateColumns = `id,name,source,domain,certificate_pem,private_key_ciphertext,fingerprint,not_before,not_after,revision,created_at,updated_at`

func scanRecord(row common.Scanner) (record, error) {
	var value record
	var notBefore, notAfter, createdAt, updatedAt common.Time
	if err := row.Scan(&value.ID, &value.Name, &value.Source, &value.Domain, &value.CertificatePEM,
		&value.PrivateKeyEncrypted, &value.Fingerprint, &notBefore, &notAfter, &value.Revision, &createdAt, &updatedAt); err != nil {
		return record{}, err
	}
	value.NotBefore, value.NotAfter = notBefore.Time, notAfter.Time
	value.CreatedAt, value.UpdatedAt = createdAt.Time, updatedAt.Time
	return value, nil
}

func (s *Store) Get(ctx context.Context, id string) (record, error) {
	value, err := scanRecord(s.db.QueryRowContext(ctx, `SELECT `+certificateColumns+` FROM certificates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, common.ErrNotFound
	}
	return value, err
}

func (s *Store) List(ctx context.Context) ([]Certificate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+certificateColumns+` FROM certificates ORDER BY name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Certificate{}
	for rows.Next() {
		value, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value.Certificate)
	}
	return values, rows.Err()
}

func (s *Store) Update(ctx context.Context, value record, expectedRevision int64) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE certificates SET name=?,source=?,domain=?,certificate_pem=?,private_key_ciphertext=?,fingerprint=?,not_before=?,not_after=?,
  revision=revision+1,updated_at=? WHERE id=? AND revision=?`, value.Name, value.Source, value.Domain, value.CertificatePEM,
		value.PrivateKeyEncrypted, value.Fingerprint, common.UnixTime(value.NotBefore), common.UnixTime(value.NotAfter),
		common.UnixTime(value.UpdatedAt), value.ID, expectedRevision)
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
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM certificates WHERE id=?`, id)
	if err != nil {
		return common.MapSQLiteError(err)
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return common.ErrNotFound
	}
	return nil
}
