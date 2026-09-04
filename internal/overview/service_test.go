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

	service := NewServiceWithTraffic(db, deployment.NewSyncService(db, deployment.ApplyConfig{}), nil)
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

	result, err := NewServiceWithTraffic(db, deployment.NewSyncService(db, deployment.ApplyConfig{}), nil).Get(t.Context())
	if err != nil {
		t.Fatalf("get overview: %v", err)
	}
	if result.RuntimeStatus != "unhealthy" || result.ConfigStatus != "not_configured" {
		t.Fatalf("runtime result = %#v", result)
	}
}

func TestGetIncludesProviderBandwidthWhenAvailable(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	service := NewServiceWithTraffic(db, deployment.NewSyncService(db, deployment.ApplyConfig{}), nil)
	if result, err := service.Get(t.Context()); err != nil || result.Bandwidth != nil {
		t.Fatalf("未注入数据源时 bandwidth 应为 nil：result=%+v err=%v", result.Bandwidth, err)
	}

	updatedAt := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var available bool
	service.SetBandwidthSource(func() (Bandwidth, bool) {
		if !available {
			return Bandwidth{}, false
		}
		return Bandwidth{UsedBytes: 25292189863, TotalBytes: 2147483648000, Percent: 1.18, UpdatedAt: updatedAt}, true
	})
	available = true
	result, err := service.Get(t.Context())
	if err != nil {
		t.Fatalf("get overview: %v", err)
	}
	if result.Bandwidth == nil {
		t.Fatal("注入数据源后 bandwidth 不应为 nil")
	}
	if result.Bandwidth.UsedBytes != 25292189863 || result.Bandwidth.TotalBytes != 2147483648000 || !result.Bandwidth.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("bandwidth=%+v", result.Bandwidth)
	}

	// 数据源已配置但尚无采样（首轮轮询前）同样回落为 nil。
	available = false
	if result, err := service.Get(t.Context()); err != nil || result.Bandwidth != nil {
		t.Fatalf("无采样时 bandwidth 应为 nil：result=%+v err=%v", result.Bandwidth, err)
	}
}
