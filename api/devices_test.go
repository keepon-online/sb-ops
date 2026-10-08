package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	controlaudit "github.com/keepon-online/sb-ops/internal/audit"
	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	controltraffic "github.com/keepon-online/sb-ops/internal/traffic"
)

func TestUserDeviceAPILifecycle(t *testing.T) {
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
	userService := serviceuser.NewService(serviceuser.NewStore(db))
	auditService := controlaudit.NewService(db)

	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{
		Identity:  identityService,
		Store:     identityStore,
		Users:     userService,
		AuditLogs: auditService,
		Traffic:   controltraffic.NewService(db),
		Config:    config.DefaultRuntimeConfig,
	})

	// 1. 登录
	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)

	// 2. 创建用户
	createUser := performJSON(t, router, http.MethodPost, "/api/v1/users", `{"name":"test-user","enabled":true}`, session.Token)
	if createUser.Code != http.StatusCreated {
		t.Fatalf("create user: status=%d body=%s", createUser.Code, createUser.Body.String())
	}
	var createdUser userMutationResponse
	decodeResponse(t, createUser, &createdUser)
	userID := createdUser.User.ID

	// 3. 手动添加设备
	createDev := performJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/devices", `{"name":"书房 · Verge"}`, session.Token)
	if createDev.Code != http.StatusCreated {
		t.Fatalf("create device: status=%d body=%s", createDev.Code, createDev.Body.String())
	}
	var devResp deviceMutationResponse
	decodeResponse(t, createDev, &devResp)
	if devResp.Device.Name != "书房 · Verge" || devResp.Device.Auto != false || devResp.Credential == nil || devResp.Credential.Token == "" {
		t.Fatalf("unexpected created device response: %+v", devResp)
	}
	deviceID := devResp.Device.ID

	// 4. 重复名称添加设备被 409 拦截
	dupDev := performJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/devices", `{"name":"书房 · Verge"}`, session.Token)
	if dupDev.Code != http.StatusConflict {
		t.Fatalf("duplicate device: status=%d want %d body=%s", dupDev.Code, http.StatusConflict, dupDev.Body.String())
	}

	// 5. 空名称校验失败 400
	emptyDev := performJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/devices", `{"name":"   "}`, session.Token)
	if emptyDev.Code != http.StatusBadRequest {
		t.Fatalf("empty device name: status=%d want %d body=%s", emptyDev.Code, http.StatusBadRequest, emptyDev.Body.String())
	}

	// 6. 重命名设备与修改启用状态，校验 204
	patchDev := performJSON(t, router, http.MethodPatch, "/api/v1/users/"+userID+"/devices/"+deviceID, `{"name":"客厅 · Verge","enabled":false}`, session.Token)
	if patchDev.Code != http.StatusNoContent {
		t.Fatalf("patch device: status=%d body=%s", patchDev.Code, patchDev.Body.String())
	}

	// 7. 查询设备列表，校验名称、Auto 标识、解密的 Token 与历史累计流量
	if _, err := db.Exec(`INSERT INTO traffic_hourly(bucket_start,node_id,user_id,protocol_id,device_id,up_bytes,down_bytes,sample_count,updated_at)
VALUES(1700000000,'local',?,'',?,1024,2048,1,1700000000)`, userID, deviceID); err != nil {
		t.Fatalf("seed traffic_hourly: %v", err)
	}
	listDev := performJSON(t, router, http.MethodGet, "/api/v1/users/"+userID+"/devices", "", session.Token)
	if listDev.Code != http.StatusOK {
		t.Fatalf("list devices: status=%d body=%s", listDev.Code, listDev.Body.String())
	}
	var listResp struct {
		Items []struct {
			serviceuser.Device
			LifetimeBytes int64 `json:"lifetimeBytes"`
		} `json:"items"`
		UserLifetimeBytes *int64 `json:"userLifetimeBytes"`
	}
	decodeResponse(t, listDev, &listResp)
	if len(listResp.Items) != 1 {
		t.Fatalf("devices count=%d want 1", len(listResp.Items))
	}
	got := listResp.Items[0]
	if got.Name != "客厅 · Verge" || got.Enabled != false || got.Auto != false || got.Token == "" {
		t.Fatalf("unexpected device in list: %+v", got)
	}
	if got.LifetimeBytes != 3072 {
		t.Fatalf("device lifetimeBytes = %d, want 3072", got.LifetimeBytes)
	}
	if listResp.UserLifetimeBytes == nil || *listResp.UserLifetimeBytes != 3072 {
		t.Fatalf("userLifetimeBytes = %v, want 3072", listResp.UserLifetimeBytes)
	}

	// 8. 校验更新审计日志中记录了字段级 diff
	logs := performJSON(t, router, http.MethodGet, "/api/v1/audit-logs?action=user.device.update&result=success", "", session.Token)
	if logs.Code != http.StatusOK {
		t.Fatalf("audit logs: status=%d body=%s", logs.Code, logs.Body.String())
	}
	var page struct {
		Items []struct {
			Details json.RawMessage `json:"details"`
		} `json:"items"`
	}
	decodeResponse(t, logs, &page)
	if len(page.Items) == 0 {
		t.Fatal("missing user.device.update audit log")
	}
	var details struct {
		Changes map[string]struct {
			Old any `json:"old"`
			New any `json:"new"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(page.Items[0].Details, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	nameChange, ok := details.Changes["name"]
	if !ok || nameChange.Old != "书房 · Verge" || nameChange.New != "客厅 · Verge" {
		t.Fatalf("name diff = %+v", details.Changes)
	}
	enabledChange, ok := details.Changes["enabled"]
	if !ok || enabledChange.Old != true || enabledChange.New != false {
		t.Fatalf("enabled diff = %+v", details.Changes)
	}
}
