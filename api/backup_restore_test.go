package api

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
	servicebackup "github.com/keepon-online/sb-ops/internal/service/backup"
	controlupgrade "github.com/keepon-online/sb-ops/internal/service/upgrade"
)

// restoreFixture 组装单次恢复端点测试所需的路由与状态；恢复触发后服务进入
// 不可复用的停机语义（restoring 不复位，进程即将重启），因此每个触发类
// 场景都需要独立 fixture。
type restoreFixture struct {
	router  *gin.Engine
	backups *servicebackup.Service
	actions *[]func() error
	token   string
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
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
	dataDir := t.TempDir()
	actions := make([]func() error, 0, 1)
	backups := servicebackup.NewService(db, func() string { return dataDir }, func() string { return filepath.Join(dataDir, "singbox-web.db") }, func(action func() error) {
		actions = append(actions, action)
	})
	upgrade := controlupgrade.NewService(func() (string, string) { return "", "" }, "v0.5.5", nil, nil)
	router := gin.New()
	router.Use(httpx.RequestIDMiddleware())
	RegisterRoutes(router, Dependencies{Identity: identityService, Store: identityStore, Upgrade: upgrade, Backup: backups})

	login := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"admin123"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session loginResponse
	decodeResponse(t, login, &session)
	return &restoreFixture{router: router, backups: backups, actions: &actions, token: session.Token}
}

// performMultipart 以 multipart/form-data 的 file 字段提交内容，照
// performJSON 的认证头约定。
func performMultipart(t *testing.T, router http.Handler, path string, content []byte, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "sb-ops-backup-20260909-040000.db")
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("写入 multipart 失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Request-ID", "request-12345678")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// snapshotBytes 生成与生产备份同构的合法快照字节。
func snapshotBytes(t *testing.T) []byte {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("迁移内存库失败: %v", err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatalf("生成快照失败: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	return content
}

func TestRestoreUploadEndpoint(t *testing.T) {
	fixture := newRestoreFixture(t)

	if rec := performMultipart(t, fixture.router, "/api/v1/system/restore", snapshotBytes(t), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", rec.Code, rec.Body.String())
	}

	t.Run("非法内容回 400 且不触发", func(t *testing.T) {
		rec := performMultipart(t, fixture.router, "/api/v1/system/restore", []byte("not a database"), fixture.token)
		assertErrorCode(t, rec, http.StatusBadRequest, httpx.CodeBackupInvalid)
		if len(*fixture.actions) != 0 {
			t.Fatalf("校验失败不应触发恢复")
		}
	})

	t.Run("合法快照受理恢复", func(t *testing.T) {
		rec := performMultipart(t, fixture.router, "/api/v1/system/restore", snapshotBytes(t), fixture.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(*fixture.actions) != 1 {
			t.Fatalf("应注册一个换文件动作，实际 %d", len(*fixture.actions))
		}
	})
}

func TestRestoreExistingEndpoint(t *testing.T) {
	fixture := newRestoreFixture(t)

	t.Run("不存在的备份回 404", func(t *testing.T) {
		rec := performJSON(t, fixture.router, http.MethodPost, "/api/v1/system/backups/sb-ops-backup-20990101-000000.db/restore", "", fixture.token)
		assertErrorCode(t, rec, http.StatusNotFound, httpx.CodeNotFound)
	})

	t.Run("已有备份受理恢复且原件保留", func(t *testing.T) {
		created, err := fixture.backups.Create(context.Background())
		if err != nil {
			t.Fatalf("创建备份失败: %v", err)
		}
		rec := performJSON(t, fixture.router, http.MethodPost, "/api/v1/system/backups/"+created.Name+"/restore", "", fixture.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(*fixture.actions) != 1 {
			t.Fatalf("应注册一个换文件动作，实际 %d", len(*fixture.actions))
		}
		values, err := fixture.backups.List()
		if err != nil || len(values) == 0 {
			t.Fatalf("备份列表原件应保留: %v", err)
		}
	})
}
