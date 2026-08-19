package identity

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateAccount inserts a single administrator account and records the setup
// state. It is used by EnsureBootstrapAdmin (test fixtures); production
// initialization goes through InitializeAdmin.
func (s *Store) CreateAccount(ctx context.Context, account Account) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO accounts (
  id, username, password_hash, enabled, must_change_password, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID, strings.TrimSpace(account.Username), account.PasswordHash, account.Enabled,
		0, account.Revision, common.UnixTime(account.CreatedAt), common.UnixTime(account.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("插入账号: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO setup_state (id, initialized_at) VALUES (1, ?)`, common.UnixTime(account.CreatedAt)); err != nil {
		return fmt.Errorf("记录初始化状态: %w", err)
	}
	return tx.Commit()
}

func (s *Store) AccountByUsername(ctx context.Context, username string) (Account, error) {
	return s.account(ctx, `WHERE username = ?`, strings.TrimSpace(username))
}

func (s *Store) AccountByID(ctx context.Context, id string) (Account, error) {
	return s.account(ctx, `WHERE id = ?`, id)
}

func (s *Store) AccountCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM accounts`).Scan(&count)
	return count, err
}

func (s *Store) Initialized(ctx context.Context) (bool, error) {
	var initialized int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM setup_state WHERE id=1`).Scan(&initialized)
	return initialized == 1, err
}

func (s *Store) InitializeAdmin(ctx context.Context, account Account) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO setup_state (id, initialized_at) VALUES (1, ?)`, common.UnixTime(account.CreatedAt))
	if err != nil {
		return err
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if claimed == 0 {
		return ErrAlreadyInitialized
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO accounts (
  id, username, password_hash, enabled, must_change_password, revision, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID, account.Username, account.PasswordHash, account.Enabled,
		0, account.Revision, common.UnixTime(account.CreatedAt), common.UnixTime(account.UpdatedAt),
	); err != nil {
		return fmt.Errorf("插入初始账号: %w", err)
	}
	return tx.Commit()
}

func (s *Store) account(ctx context.Context, where string, value any) (Account, error) {
	var account Account
	var createdAt, updatedAt common.Time
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, enabled, revision, created_at, updated_at
FROM accounts `+where, value).Scan(
		&account.ID, &account.Username, &account.PasswordHash, &account.Enabled,
		&account.Revision, &createdAt, &updatedAt,
	)
	if err != nil {
		return Account{}, err
	}
	account.CreatedAt, account.UpdatedAt = createdAt.Time, updatedAt.Time
	return account, nil
}

func (s *Store) SaveSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO sessions (
  id, account_id, token_hash, client_ip, user_agent, expires_at, revoked_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.AccountID, session.TokenHash, session.ClientIP, session.UserAgent,
		common.UnixTime(session.ExpiresAt), common.TimeFromPtr(session.RevokedAt), common.UnixTime(session.CreatedAt),
	)
	return err
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash []byte) (Session, error) {
	var session Session
	var expiresAt, revokedAt, createdAt common.Time
	err := s.db.QueryRowContext(ctx, `
SELECT id, account_id, token_hash, client_ip, user_agent, expires_at, revoked_at, created_at
FROM sessions WHERE token_hash = ?`, tokenHash).Scan(
		&session.ID, &session.AccountID, &session.TokenHash, &session.ClientIP, &session.UserAgent,
		&expiresAt, &revokedAt, &createdAt,
	)
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt = expiresAt.Time
	session.CreatedAt = createdAt.Time
	session.RevokedAt = revokedAt.Ptr()
	return session, nil
}

func (s *Store) ChangePasswordAndRevokeSessions(
	ctx context.Context,
	accountID string,
	expectedRevision int64,
	passwordHash []byte,
	updatedAt time.Time,
) (Account, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE accounts
SET password_hash=?, revision=revision+1, updated_at=?
WHERE id=? AND revision=?`,
		passwordHash, common.UnixTime(updatedAt), accountID, expectedRevision,
	)
	if err != nil {
		return Account{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Account{}, err
	}
	if changed == 0 {
		return Account{}, ErrRevisionConflict
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE sessions SET revoked_at=? WHERE account_id=? AND revoked_at IS NULL`,
		common.UnixTime(updatedAt), accountID,
	); err != nil {
		return Account{}, err
	}
	if err := tx.Commit(); err != nil {
		return Account{}, err
	}
	return s.AccountByID(ctx, accountID)
}

func (s *Store) RevokeSessionByTokenHash(ctx context.Context, tokenHash []byte, revokedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL`,
		common.UnixTime(revokedAt), tokenHash,
	)
	return err
}

// DeleteStaleSessions removes sessions that expired or were revoked before the
// cutoff. Authentication never matches them again, so the table would otherwise
// grow without bound over the process lifetime.
func (s *Store) DeleteStaleSessions(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
DELETE FROM sessions WHERE expires_at<? OR (revoked_at IS NOT NULL AND revoked_at<?)`,
		common.UnixTime(cutoff), common.UnixTime(cutoff),
	)
	if err != nil {
		return 0, err
	}
	removed, _ := result.RowsAffected()
	return removed, nil
}

func (s *Store) AppendAuditLog(ctx context.Context, entry AuditLog) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_logs (
  id, actor_id, action, resource_type, resource_id, result,
  request_id, client_ip, details_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, optionalString(entry.ActorID), entry.Action, entry.ResourceType, entry.ResourceID,
		entry.Result, entry.RequestID, entry.ClientIP, entry.DetailsJSON, common.UnixTime(entry.CreatedAt),
	)
	return err
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
