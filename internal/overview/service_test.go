package overview

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
	deployment "github.com/keepon-online/sb-ops/internal/deployment/singlevps"
	_ "github.com/mattn/go-sqlite3"
)

func TestGetExplainsUnconfiguredRuntimeAndOnboarding(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	service := NewService(db, deployment.NewSyncService(db, deployment.ApplyConfig{}))
	result, err := service.Get(t.Context())
	if err != nil {
		t.Fatalf("get overview: %v", err)
	}
	if result.RuntimeStatus != "not_configured" || result.ConfigStatus != "not_configured" || !strings.Contains(result.RuntimeReason, "公网地址") {
		t.Fatalf("runtime result = %#v", result)
	}
	if len(result.Tasks) != 4 || result.Tasks[0].Completed || result.Tasks[1].Completed || result.Tasks[2].Completed || result.Tasks[3].Completed {
		t.Fatalf("onboarding tasks = %#v", result.Tasks)
	}
}

func TestGetPrioritizesRecentSyncFailureOverUnconfiguredAddress(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	failedAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO runtime_state(id,config_digest,last_applied_at,last_failed_at,failure_reason,artifact_count) VALUES(1,'',NULL,?,'start embedded sing-box: invalid configuration',0)`, failedAt.Unix()); err != nil {
		t.Fatalf("store failed runtime state: %v", err)
	}

	result, err := NewService(db, deployment.NewSyncService(db, deployment.ApplyConfig{})).Get(t.Context())
	if err != nil {
		t.Fatalf("get overview: %v", err)
	}
	if result.RuntimeStatus != "unhealthy" || result.ConfigStatus != "not_configured" {
		t.Fatalf("runtime result = %#v", result)
	}
}
