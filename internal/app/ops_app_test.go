package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/identity"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
	_ "github.com/mattn/go-sqlite3"
)

func TestNewOpsApplicationServesV1Identity(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	app, err := NewOps(db, time.Hour)
	if err != nil {
		t.Fatalf("NewOps: %v", err)
	}
	defer app.Close()
	policies, err := app.Policies.List(t.Context())
	if err != nil || len(policies) != 1 || policies[0].Name != "Clash 默认分流" {
		t.Fatalf("default policies = %#v, err=%v", policies, err)
	}
	rules, err := app.Policies.ListRules(t.Context(), policies[0].ID)
	if err != nil || len(rules) != 16 {
		t.Fatalf("default rules = %#v, err=%v", rules, err)
	}

	rec := httptest.NewRecorder()
	app.Router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"initialized":false`) {
		t.Fatalf("setup status code = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"admin123"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("uninitialized login code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTrafficStallErrorDistinguishesIdleFromBrokenCollection(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := identity.NewStore(db)
	if _, err := identity.NewService(store, time.Hour).InitializeAdmin(t.Context(), "admin", "admin123456"); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	// A literal OpsApp keeps the background loops out of the test, so the
	// connection probe can be swapped without racing the operations cycle.
	value := &OpsApp{IdentityStore: store, Traffic: controltraffic.NewService(db)}

	// The collection loop never recorded a cycle, so the stall condition holds
	// throughout the test; only the connection probe varies.
	for _, testCase := range []struct {
		name        string
		connections int
		wantErr     bool
	}{
		{"idle box suppresses the stall error", 0, false},
		{"active connections without a heartbeat report the stall", 3, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			value.activeConnections = func(context.Context) int { return testCase.connections }
			err := value.trafficStallError(t.Context())
			if (err != nil) != testCase.wantErr {
				t.Fatalf("trafficStallError() = %v, wantErr=%v", err, testCase.wantErr)
			}
		})
	}

	// A fresh heartbeat clears the stall even with active connections.
	value.Traffic.RecordCollection()
	value.activeConnections = func(context.Context) int { return 3 }
	if err := value.trafficStallError(t.Context()); err != nil {
		t.Fatalf("trafficStallError() with fresh heartbeat = %v, want nil", err)
	}
}
