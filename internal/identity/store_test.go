package identity

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
)

func TestStoreCreatesAndFindsAccount(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	account := Account{
		ID:           "account-1",
		Username:     "Admin",
		PasswordHash: []byte("password-hash"),
		Enabled:      true,
		Revision:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := store.CreateAccount(t.Context(), account); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	found, err := store.AccountByUsername(t.Context(), " admin ")
	if err != nil {
		t.Fatalf("AccountByUsername: %v", err)
	}
	if found.ID != account.ID || found.Username != "Admin" || !found.Enabled || found.Revision != 1 {
		t.Fatalf("account=%+v", found)
	}
}

func TestStorePersistsOnlySessionTokenHash(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 7, 14, 8, 0, 0, 0, time.UTC)
	if err := store.CreateAccount(t.Context(), Account{
		ID: "account-1", Username: "admin", PasswordHash: []byte("hash"), Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	tokenHash := bytes.Repeat([]byte{0x2a}, 32)
	session := Session{
		ID: "session-1", AccountID: "account-1", TokenHash: tokenHash,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := store.SaveSession(t.Context(), session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	found, err := store.SessionByTokenHash(t.Context(), tokenHash)
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if found.ID != session.ID || found.AccountID != session.AccountID || !bytes.Equal(found.TokenHash, tokenHash) {
		t.Fatalf("session=%+v", found)
	}

	var tokenColumnCount int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('sessions') WHERE name='token'`).Scan(&tokenColumnCount); err != nil {
		t.Fatalf("inspect sessions: %v", err)
	}
	if tokenColumnCount != 0 {
		t.Fatal("sessions must not contain a plaintext token column")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return NewStore(db)
}
