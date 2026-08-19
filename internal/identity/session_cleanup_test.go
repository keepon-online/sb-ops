package identity

import (
	"bytes"
	"testing"
	"time"
)

func TestStoreDeleteStaleSessions(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	if err := store.CreateAccount(t.Context(), Account{
		ID: "account-1", Username: "admin", PasswordHash: []byte("hash"), Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	revoked := now.Add(-time.Hour)
	expired := Session{
		ID: "session-expired", AccountID: "account-1", TokenHash: bytes.Repeat([]byte{0x01}, 32),
		ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-2 * time.Hour),
	}
	revokedSession := Session{
		ID: "session-revoked", AccountID: "account-1", TokenHash: bytes.Repeat([]byte{0x02}, 32),
		ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked, CreatedAt: now.Add(-2 * time.Hour),
	}
	active := Session{
		ID: "session-active", AccountID: "account-1", TokenHash: bytes.Repeat([]byte{0x03}, 32),
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	for _, session := range []Session{expired, revokedSession, active} {
		if err := store.SaveSession(t.Context(), session); err != nil {
			t.Fatalf("SaveSession(%s): %v", session.ID, err)
		}
	}

	removed, err := store.DeleteStaleSessions(t.Context(), now)
	if err != nil {
		t.Fatalf("DeleteStaleSessions: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed=%d want 2", removed)
	}
	if _, err := store.SessionByTokenHash(t.Context(), active.TokenHash); err != nil {
		t.Fatalf("active session should survive: %v", err)
	}
	if _, err := store.SessionByTokenHash(t.Context(), expired.TokenHash); err == nil {
		t.Fatal("expired session should be gone")
	}
	again, err := store.DeleteStaleSessions(t.Context(), now)
	if err != nil || again != 0 {
		t.Fatalf("second pass removed=%d err=%v want 0", again, err)
	}
}
