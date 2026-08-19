package identity

import (
	"testing"
	"time"
)

func TestStoreAppendsAuditLogFields(t *testing.T) {
	store := newTestStore(t)
	actorID := "account-1"
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	if err := store.CreateAccount(t.Context(), Account{
		ID: actorID, Username: "admin", PasswordHash: []byte("hash"), Enabled: true,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	entry := AuditLog{
		ID: "audit-1", ActorID: &actorID, Action: "auth.login",
		ResourceType: "account", ResourceID: actorID, Result: "success",
		RequestID: "request-123", ClientIP: "127.0.0.1", DetailsJSON: `{"safe":true}`,
		CreatedAt: now,
	}
	if err := store.AppendAuditLog(t.Context(), entry); err != nil {
		t.Fatalf("AppendAuditLog: %v", err)
	}

	var actor, details string
	var createdAt int64
	err := store.db.QueryRow(`SELECT actor_id, details_json, created_at FROM audit_logs WHERE id = ?`, entry.ID).
		Scan(&actor, &details, &createdAt)
	if err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if actor != actorID {
		t.Fatalf("actor_id=%q", actor)
	}
	if details != `{"safe":true}` {
		t.Fatalf("details_json=%q", details)
	}
	if createdAt != now.Unix() {
		t.Fatalf("created_at=%d want %d", createdAt, now.Unix())
	}
}
