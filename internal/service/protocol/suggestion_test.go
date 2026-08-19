package protocol

import (
	"database/sql"
	"testing"

	resource "github.com/keepon-online/sb-ops/internal/service"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	_ "github.com/mattn/go-sqlite3"
)

func TestSuggestPortSeparatesTCPAndUDPListeners(t *testing.T) {
	db, service := newTestProtocolService(t)
	if _, err := service.Create(t.Context(), WriteInput{
		Name: "TCP 8443", Type: TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 8443,
		Settings: map[string]any{"method": "2022-blake3-aes-128-gcm"},
	}); err != nil {
		t.Fatalf("create TCP protocol: %v", err)
	}

	tcp, err := service.SuggestPort(t.Context(), TypeVLESS)
	if err != nil {
		t.Fatalf("suggest TCP port: %v", err)
	}
	if tcp.ListenPort != 2053 {
		t.Fatalf("TCP port=%d, want 2053", tcp.ListenPort)
	}
	udp, err := service.SuggestPort(t.Context(), TypeHysteria2)
	if err != nil {
		t.Fatalf("suggest UDP port: %v", err)
	}
	if udp.ListenPort != 8443 {
		t.Fatalf("UDP port=%d, want 8443", udp.ListenPort)
	}

	if _, err := db.Exec(`UPDATE protocols SET enabled=0 WHERE name='TCP 8443'`); err != nil {
		t.Fatalf("disable TCP protocol: %v", err)
	}
	available, err := service.SuggestPort(t.Context(), TypeVLESS)
	if err != nil {
		t.Fatalf("suggest released TCP port: %v", err)
	}
	if available.ListenPort != 8443 {
		t.Fatalf("released TCP port=%d, want 8443", available.ListenPort)
	}
}

func newTestProtocolService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	// database 包现在依赖 argo（间接依赖本包），测试直接用 resource schema 建表，
	// 避免 import 环（与 observability/traffic 测试同款约束）。
	if _, err := db.Exec(resource.Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db, NewService(NewStore(db, secret.NewStore(db)))
}
