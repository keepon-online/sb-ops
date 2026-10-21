package singlevps

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
	deploymentcompiler "github.com/keepon-online/sb-ops/internal/deployment/compiler"
)

func TestSaveAppliedReplacesCurrentArtifacts(t *testing.T) {
	db := testSyncDatabase(t)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	first := SyncResult{ConfigDigest: "first", ArtifactCount: 1, AppliedAt: now}
	if err := saveApplied(t.Context(), db, first, []deploymentcompiler.Subscription{{UserID: "user-a", DeviceID: "device-a", Content: []byte("first")}}); err != nil {
		t.Fatalf("save first state: %v", err)
	}
	second := SyncResult{ConfigDigest: "second", ArtifactCount: 1, AppliedAt: now.Add(time.Minute)}
	if err := saveApplied(t.Context(), db, second, []deploymentcompiler.Subscription{{UserID: "user-b", DeviceID: "device-b", Content: []byte("second")}}); err != nil {
		t.Fatalf("save second state: %v", err)
	}

	var digest string
	var artifactCount int
	if err := db.QueryRow(`SELECT config_digest,artifact_count FROM runtime_state WHERE id=1`).Scan(&digest, &artifactCount); err != nil {
		t.Fatalf("query runtime state: %v", err)
	}
	if digest != "second" || artifactCount != 1 {
		t.Fatalf("runtime state digest=%q artifacts=%d", digest, artifactCount)
	}
	var userID, deviceID string
	var content []byte
	if err := db.QueryRow(`SELECT user_id,device_id,content FROM runtime_artifacts`).Scan(&userID, &deviceID, &content); err != nil {
		t.Fatalf("query artifact: %v", err)
	}
	if userID != "user-b" || deviceID != "device-b" || string(content) != "second" {
		t.Fatalf("artifact user=%q content=%q", userID, content)
	}
}

func TestRecordFailurePreservesAppliedState(t *testing.T) {
	db := testSyncDatabase(t)
	appliedAt := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	if err := saveApplied(t.Context(), db, SyncResult{ConfigDigest: "current", ArtifactCount: 1, AppliedAt: appliedAt}, []deploymentcompiler.Subscription{{UserID: "user-a", DeviceID: "device-a", Content: []byte("current")}}); err != nil {
		t.Fatalf("save applied state: %v", err)
	}
	syncer := NewSyncService(db, ApplyConfig{})
	syncer.now = func() time.Time { return appliedAt.Add(time.Minute) }
	if err := syncer.recordFailure(context.Background(), sql.ErrConnDone); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	var failedDigest, failedReason string
	var failedApplied int64
	if err := db.QueryRow(`SELECT config_digest,last_applied_at,failure_reason FROM runtime_state WHERE id=1`).Scan(&failedDigest, &failedApplied, &failedReason); err != nil {
		t.Fatalf("query failed state: %v", err)
	}
	if failedDigest != "current" || failedApplied != appliedAt.Unix() || failedReason == "" {
		t.Fatalf("failed state digest=%q applied=%d failure=%q", failedDigest, failedApplied, failedReason)
	}
	if err := saveApplied(t.Context(), db, SyncResult{ConfigDigest: "recovered", ArtifactCount: 1, AppliedAt: appliedAt.Add(2 * time.Minute)}, []deploymentcompiler.Subscription{{UserID: "user-a", DeviceID: "device-a", Content: []byte("recovered")}}); err != nil {
		t.Fatalf("save recovered state: %v", err)
	}

	var digest, failure string
	var persistedApplied int64
	var failed sql.NullInt64
	if err := db.QueryRow(`SELECT config_digest,last_applied_at,last_failed_at,failure_reason FROM runtime_state WHERE id=1`).Scan(&digest, &persistedApplied, &failed, &failure); err != nil {
		t.Fatalf("query state: %v", err)
	}
	if digest != "recovered" || persistedApplied != appliedAt.Add(2*time.Minute).Unix() || failure != "" || failed.Valid {
		t.Fatalf("recovered state digest=%q applied=%d failed=%d failure=%q", digest, persistedApplied, failed.Int64, failure)
	}
}

func TestClashAPIResolvedFromManagedConfig(t *testing.T) {
	syncer := NewSyncService(testSyncDatabase(t), ApplyConfig{})
	endpoint, _, err := syncer.clashAPI(t.Context())
	if err != nil {
		t.Fatalf("clashAPI: %v", err)
	}
	if endpoint != "http://127.0.0.1:9090" {
		t.Fatalf("endpoint=%q, want http://127.0.0.1:9090", endpoint)
	}
	if status := syncer.RuntimeStatus(t.Context()); !status.CanObserveConns {
		t.Fatalf("expected connections observable from managed config, status=%#v", status)
	}
}

func TestClashAPIReportsUnavailableWhenDisabled(t *testing.T) {
	db := testSyncDatabase(t)
	syncer := NewSyncService(db, ApplyConfig{})
	// The row is created lazily by GetConfig; seed it before disabling so the
	// UPDATE is not a no-op.
	if _, err := db.Exec(`INSERT OR IGNORE INTO singbox_config(id,updated_at) VALUES(1,'2026-07-28T00:00:00Z')`); err != nil {
		t.Fatalf("seed singbox config: %v", err)
	}
	if _, err := db.Exec(`UPDATE singbox_config SET clash_api_enabled=0 WHERE id=1`); err != nil {
		t.Fatalf("disable clash API: %v", err)
	}
	if _, _, err := syncer.clashAPI(t.Context()); err == nil {
		t.Fatal("expected error when the Clash API is disabled")
	}
	if status := syncer.RuntimeStatus(t.Context()); status.CanObserveConns {
		t.Fatalf("expected connections unavailable when Clash API is disabled, status=%#v", status)
	}
}

func testSyncDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users(id,name,enabled,quota_bytes,used_bytes,reset_interval_days,revision,created_at,updated_at) VALUES('user-a','User A',1,0,0,0,1,'2026-07-28T00:00:00Z','2026-07-28T00:00:00Z'),('user-b','User B',1,0,0,0,1,'2026-07-28T00:00:00Z','2026-07-28T00:00:00Z')`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO devices(id,user_id,name,enabled,used_bytes,rotated_at,created_at) VALUES('device-a','user-a','默认设备',1,0,'2026-07-28T00:00:00Z','2026-07-28T00:00:00Z'),('device-b','user-b','默认设备',1,0,'2026-07-28T00:00:00Z','2026-07-28T00:00:00Z')`); err != nil {
		t.Fatalf("seed devices: %v", err)
	}
	return db
}
