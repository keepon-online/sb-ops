package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
)

// 更新类操作的成功审计必须带字段级 diff（details.changes.{field}.{old,new}），
// 否则排查「谁把配置改坏了」只能靠猜——这是 2026-08 订阅兜底被改成 direct
// 一周无人察觉的直接教训。
func TestUpdatePolicyAuditRecordsFieldChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	identityStore := identity.NewStore(db)
	identityService := identity.NewService(identityStore, time.Hour)
	if _, err := identityService.EnsureBootstrapAdmin(t.Context(), "admin", "admin123"); err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{
		Identity: identityService, Store: identityStore,
		Policies:  servicepolicy.NewService(servicepolicy.NewStore(db)),
		AuditLogs: controlaudit.NewService(db),
	})

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)

	create := performJSON(t, router, http.MethodPost, "/api/v1/policies", `{"name":"测试策略","enabled":true,"defaultAction":"direct"}`, session.Token)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var created servicepolicy.Policy
	decodeResponse(t, create, &created)

	update := performJSON(t, router, http.MethodPatch, "/api/v1/policies/"+created.ID,
		`{"revision":1,"name":"测试策略","enabled":true,"defaultAction":"proxy"}`, session.Token)
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}

	logs := performJSON(t, router, http.MethodGet, "/api/v1/audit-logs?action=policy.update&result=success", "", session.Token)
	if logs.Code != http.StatusOK {
		t.Fatalf("audit status=%d body=%s", logs.Code, logs.Body.String())
	}
	var page struct {
		Items []struct {
			Details json.RawMessage `json:"details"`
		} `json:"items"`
	}
	decodeResponse(t, logs, &page)
	if len(page.Items) == 0 {
		t.Fatal("audit log missing policy.update entry")
	}
	var details struct {
		Changes map[string]struct {
			Old any `json:"old"`
			New any `json:"new"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(page.Items[0].Details, &details); err != nil {
		t.Fatalf("decode details %s: %v", page.Items[0].Details, err)
	}
	change, ok := details.Changes["defaultAction"]
	if !ok || change.Old != "direct" || change.New != "proxy" {
		t.Fatalf("defaultAction diff = %+v, want old=direct new=proxy", details.Changes)
	}
	if _, noisy := details.Changes["revision"]; noisy {
		t.Fatalf("diff should skip bookkeeping fields: %s", page.Items[0].Details)
	}
}
