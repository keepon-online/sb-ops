package audit

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	// accounts 是 actor_id 外键的父表，最小化建表即可。
	if _, err := db.Exec(`CREATE TABLE accounts (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create accounts: %v", err)
	}
	if _, err := db.Exec(Schema); err != nil {
		t.Fatalf("apply audit schema: %v", err)
	}
	return NewStore(db)
}

func TestStoreAppendAndList(t *testing.T) {
	store := newTestStore(t)
	actorID := "account-1"
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	if err := store.Append(t.Context(), Record{
		ID: "audit-1", ActorID: &actorID, Action: "policy.update",
		ResourceType: "policy", ResourceID: "policy-1", Result: "success",
		RequestID: "request-123", ClientIP: "127.0.0.1", DetailsJSON: `{"safe":true}`,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Append(t.Context(), Record{
		ID: "audit-2", Action: "auth.login",
		ResourceType: "account", Result: "failure",
		DetailsJSON: `{"message":"用户名或密码错误"}`,
		CreatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var actor, details string
	var createdAt int64
	err := store.db.QueryRow(`SELECT actor_id, details_json, created_at FROM audit_logs WHERE id = ?`, "audit-1").
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

	page, err := store.List(t.Context(), Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("total=%d items=%d", page.Total, len(page.Items))
	}
	if page.Items[0].ID != "audit-2" || page.Items[1].ID != "audit-1" {
		t.Fatalf("order = %s,%s want audit-2,audit-1", page.Items[0].ID, page.Items[1].ID)
	}
}

func TestStoreListFiltersByTimeRange(t *testing.T) {
	store := newTestStore(t)
	base := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	for _, entry := range []Record{
		{ID: "old-r", Action: "a", ResourceType: "t", Result: "success", CreatedAt: base.Add(-48 * time.Hour)},
		{ID: "mid-r", Action: "a", ResourceType: "t", Result: "success", CreatedAt: base},
		{ID: "new-r", Action: "a", ResourceType: "t", Result: "success", CreatedAt: base.Add(48 * time.Hour)},
	} {
		if err := store.Append(t.Context(), entry); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// from/to 为 RFC3339；历史上字符串直比 INTEGER 列恒真，此处锁定修复。
	page, err := store.List(t.Context(), Query{
		From: base.Add(-24 * time.Hour).Format(time.RFC3339),
		To:   base.Add(24 * time.Hour).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "mid-r" {
		t.Fatalf("time filter total=%d items=%+v", page.Total, page.Items)
	}
}

func TestStorePurgeBefore(t *testing.T) {
	store := newTestStore(t)
	now := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	for _, entry := range []Record{
		{ID: "old", Action: "a", ResourceType: "t", Result: "success", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "new", Action: "a", ResourceType: "t", Result: "success", CreatedAt: now},
	} {
		if err := store.Append(t.Context(), entry); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	removed, err := store.PurgeBefore(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("PurgeBefore: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d want 1", removed)
	}
	page, err := store.List(t.Context(), Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.Total != 1 || page.Items[0].ID != "new" {
		t.Fatalf("after purge total=%d first=%s", page.Total, page.Items[0].ID)
	}
}
