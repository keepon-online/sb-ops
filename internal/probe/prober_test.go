package probe

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	deploymentcompiler "github.com/keepon-online/sb-ops/internal/deployment/compiler"
	controlobservability "github.com/keepon-online/sb-ops/internal/observability"
	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	resource "github.com/keepon-online/sb-ops/internal/service"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"

	_ "github.com/mattn/go-sqlite3"
)

// newProbeTestDB 建内存库并应用 resource + probe_results schema（DDL 在
// observability 包，见其 schema.go 注释；本包测试不经 internal/database）。
func newProbeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{resource.Schema, controlobservability.ProbeSchema} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	}
	return db
}

func seedUserDevice(t *testing.T, db *sql.DB, devicePassword string) (userID, deviceID, deviceUUID string) {
	t.Helper()
	userID, deviceID, deviceUUID = "user-1", "device-1", "11111111-2222-4333-8444-555555555555"
	ciphertext, err := serviceuser.NewStore(db).EncryptSecret(t.Context(), devicePassword)
	if err != nil {
		t.Fatalf("encrypt device password: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`INSERT INTO users(id,name,enabled,created_at,updated_at) VALUES(?,?,1,?,?)`, userID, "测试用户", now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO devices(id,user_id,name,identity_uuid,password_ciphertext,rotated_at,created_at) VALUES(?,?,?,?,?,?,?)`,
		deviceID, userID, "测试设备", deviceUUID, ciphertext, now, now); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return userID, deviceID, deviceUUID
}

func seedProtocol(t *testing.T, db *sql.DB, listenPort int) serviceprotocol.Protocol {
	t.Helper()
	serverKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	protocol := serviceprotocol.Protocol{
		ID: "ss-probe", Name: "拨测用 SS", Type: serviceprotocol.TypeShadowsocks,
		Enabled: true, Listen: "::", ListenPort: listenPort,
		Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": serverKey},
	}
	if err := serviceprotocol.NewStore(db, secret.NewStore(db)).Create(t.Context(), protocol); err != nil {
		t.Fatalf("seed protocol: %v", err)
	}
	return protocol
}

// TestProbeEndToEndThroughRealKernel 是双内核集成测试：真实主内核跑编译
// 出的服务端配置（SS-2022 入站），Prober 起客户端内核经环回穿完整链路
// （mixed → SS 隧道 → 主内核 → direct → 本地探测目标）。
func TestProbeEndToEndThroughRealKernel(t *testing.T) {
	db := newProbeTestDB(t)
	listenPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("pick listen port: %v", err)
	}
	protocol := seedProtocol(t, db, listenPort)
	userID, deviceID, deviceUUID := seedUserDevice(t, db, "device-password")
	if _, err := db.Exec(`INSERT INTO user_protocols(user_id,protocol_id) VALUES(?,?)`, userID, protocol.ID); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}

	// 主内核：与服务端入站同一份协议/设备/分配数据。
	compiled, err := deploymentcompiler.Compile(deploymentcompiler.Input{
		PublicAddress: "203.0.113.10",
		SingBoxConfig: singBoxConfigForTest(),
		Protocols:     []serviceprotocol.Protocol{protocol},
		Users:         []serviceuser.ArtifactUser{{ID: userID, Enabled: true}},
		Devices:       []serviceuser.ArtifactDevice{{UserID: userID, DeviceID: deviceID, Enabled: true, UUID: deviceUUID, Password: "device-password"}},
		UserProtocols: map[string]map[string]bool{userID: {protocol.ID: true}},
	})
	if err != nil {
		t.Fatalf("compile server config: %v", err)
	}
	manager := runtimebox.New()
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Apply(t.Context(), compiled.ServerConfig); err != nil {
		t.Fatalf("apply server config: %v", err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	prober := NewProber(db, func(context.Context) (bool, string) { return true, "" })
	prober.targetURL = target.URL

	result, err := prober.Probe(t.Context(), "")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items=%d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Status != "ok" {
		t.Fatalf("status=%s error=%s, want ok（完整链路应通）", item.Status, item.Error)
	}
	if item.LatencyMS == nil {
		t.Fatal("latencyMs = nil, want 实测延迟")
	}

	stored, err := NewStore(db).List(t.Context())
	if err != nil {
		t.Fatalf("List stored results: %v", err)
	}
	if len(stored) != 1 || stored[0].ProtocolID != protocol.ID || stored[0].Status != "ok" {
		t.Fatalf("stored=%+v, want 一行 ok 结果", stored)
	}

	// 错误凭据场景：设备密码被改（服务端仍按原密码校验），拨测必须失败
	// 且带错误信息——「端口在听但链路不通」正是本功能要暴露的故障形态。
	if _, err := db.Exec(`DELETE FROM devices WHERE id=?`, deviceID); err != nil {
		t.Fatalf("remove device: %v", err)
	}
	seedUserDevice2(t, db, "another-password", deviceUUID)
	result, err = prober.Probe(t.Context(), "")
	if err != nil {
		t.Fatalf("Probe with wrong credential: %v", err)
	}
	if item := result.Items[0]; item.Status != "failed" || item.Error == "" {
		t.Fatalf("status=%s error=%q, want failed 带错误信息", item.Status, item.Error)
	}
	stored, err = NewStore(db).List(t.Context())
	if err != nil {
		t.Fatalf("List stored results: %v", err)
	}
	if stored[0].Status != "failed" {
		t.Fatalf("stored status=%s, want failed 覆盖落库", stored[0].Status)
	}
}

// seedUserDevice2 以同一设备 id、不同密码重建设备（模拟凭据错配）。
func seedUserDevice2(t *testing.T, db *sql.DB, devicePassword, deviceUUID string) {
	t.Helper()
	ciphertext, err := serviceuser.NewStore(db).EncryptSecret(t.Context(), devicePassword)
	if err != nil {
		t.Fatalf("encrypt device password: %v", err)
	}
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`INSERT INTO devices(id,user_id,name,identity_uuid,password_ciphertext,rotated_at,created_at) VALUES(?,?,?,?,?,?,?)`,
		"device-1", "user-1", "测试设备2", deviceUUID, ciphertext, now, now); err != nil {
		t.Fatalf("reseed device: %v", err)
	}
}

// TestProbeSkipsWithoutCredentials 协议没有分配用户/设备时不拨测、写
// skipped 行（而不是失败）。
func TestProbeSkipsWithoutCredentials(t *testing.T) {
	db := newProbeTestDB(t)
	listenPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("pick listen port: %v", err)
	}
	seedProtocol(t, db, listenPort)

	prober := NewProber(db, func(context.Context) (bool, string) { return true, "" })
	result, err := prober.Probe(t.Context(), "")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if item := result.Items[0]; item.Status != "skipped" {
		t.Fatalf("status=%s, want skipped（无凭据）", item.Status)
	}
	stored, err := NewStore(db).List(t.Context())
	if err != nil || len(stored) != 1 || stored[0].Status != "skipped" {
		t.Fatalf("stored=%+v err=%v, want skipped 落库", stored, err)
	}
}

// TestProbeGatesOnKernelReady 内核未就绪时整轮跳过且不落库——内核故障
// 已有 runtime_sync_failed 告警，拨测写失败行只会制造误报。
func TestProbeGatesOnKernelReady(t *testing.T) {
	db := newProbeTestDB(t)
	listenPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatalf("pick listen port: %v", err)
	}
	seedProtocol(t, db, listenPort)

	prober := NewProber(db, func(context.Context) (bool, string) { return false, "内核未运行" })
	if _, err := prober.Probe(t.Context(), ""); !errors.Is(err, ErrKernelNotReady) {
		t.Fatalf("err=%v, want ErrKernelNotReady", err)
	}
	stored, err := NewStore(db).List(t.Context())
	if err != nil || len(stored) != 0 {
		t.Fatalf("stored=%+v err=%v, want 未落库", stored, err)
	}
}

func singBoxConfigForTest() servicesingbox.Config {
	return servicesingbox.Config{
		LogLevel: "warn", LogTimestamp: true, DefaultOutboundTag: "direct",
		ClashAPIEnabled: false, V2RayAPIEnabled: false,
	}
}
