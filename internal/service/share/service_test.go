package share

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	resource "github.com/keepon-online/sb-ops/internal/service"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"

	_ "github.com/mattn/go-sqlite3"
)

// newTestService 建库（resource schema）并装配用户与订阅服务；syncHook 模拟
// 编译同步：为该用户全部设备落一份订阅产物，并统计触发次数。
func newTestService(t *testing.T) (*Service, *serviceuser.Service, *syncRecorder) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(resource.Schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	users := serviceuser.NewService(serviceuser.NewStore(db))
	recorder := &syncRecorder{db: db}
	share := NewService(db, users, recorder.sync)
	return share, users, recorder
}

type syncRecorder struct {
	db     *sql.DB
	calls  int
	userID string
}

func (r *syncRecorder) sync() error {
	r.calls++
	if r.userID == "" {
		return nil
	}
	rows, err := r.db.Query(`SELECT id FROM devices WHERE user_id=?`, r.userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var deviceIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		deviceIDs = append(deviceIDs, id)
	}
	for _, deviceID := range deviceIDs {
		if err := seedArtifact(r.db, r.userID, deviceID); err != nil {
			return err
		}
	}
	return nil
}

func seedArtifact(db *sql.DB, userID, deviceID string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO runtime_artifacts(user_id,device_id,format,content,digest,updated_at) VALUES(?,?,?,?,?,strftime('%s','now'))`,
		userID, deviceID, "clash.yaml", []byte("artifact:"+deviceID), fmt.Sprintf("digest-%s", deviceID))
	return err
}

func mustCreateUser(t *testing.T, users *serviceuser.Service) (userID, userToken string) {
	t.Helper()
	created, token, err := users.Create(t.Context(), serviceuser.CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return created.ID, token
}

func TestDeviceKeyFromUserAgent(t *testing.T) {
	cases := []struct {
		ua, key, label string
	}{
		{"clash-verge/2.1.3", "clashverge", "Clash Verge"},
		{"ClashforWindows/0.20.39", "clashforwindows", "Clash for Windows"},
		{"Clash Meta For Android/2.11.5.Meta", "clashmetaforandroid", "Clash Meta Android"},
		{"clash.meta/1.18.6", "clashmeta", "Clash Meta"},
		{"Stash/2.8.0", "stash", "Stash"},
		{"MyClient/1.0", "ua-myclient", "MyClient/1.0"},
		{"", "", ""},
	}
	for _, tc := range cases {
		key, label := DeviceKeyFromUserAgent(tc.ua)
		if key != tc.key || label != tc.label {
			t.Fatalf("UA %q → (%q,%q), want (%q,%q)", tc.ua, key, label, tc.key, tc.label)
		}
	}
}

func TestParseUserAgentMetadata(t *testing.T) {
	cases := []struct {
		ua, key, client, platform, version string
	}{
		{"Clash Meta For Android/2.11.5.Meta", "clashmetaforandroid", "Clash Meta Android", "android", "2.11.5.Meta"},
		{"Shadowrocket/2.2.66 (iPhone; iOS 17.5)", "shadowrocket", "Shadowrocket", "ios", "2.2.66"},
		{"ClashforWindows/0.20.39 (Windows)", "clashforwindows", "Clash for Windows", "windows", "0.20.39"},
		{"MyClient/1.0 (Linux)", "ua-myclient", "MyClient/1.0", "linux", "1.0"},
		{"", "", "", "unknown", ""},
	}
	for _, tc := range cases {
		got := ParseUserAgent(tc.ua)
		if got.Key != tc.key || got.Client != tc.client || got.Platform != tc.platform || got.Version != tc.version {
			t.Fatalf("UA %q → %+v, want key=%q client=%q platform=%q version=%q", tc.ua, got, tc.key, tc.client, tc.platform, tc.version)
		}
	}
}

func TestAutoDiscoveryRoutesByUserAgent(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID

	// 首次以 Clash Verge 拉取：自动建设备并同步，返回新设备产物。
	first, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3", "198.51.100.10")
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if first.DeviceID == "" || string(first.Content) != "artifact:"+first.DeviceID {
		t.Fatalf("first fetch should serve the auto device artifact: %+v", first)
	}
	if recorder.calls != 1 {
		t.Fatalf("sync calls=%d, want 1", recorder.calls)
	}
	var platform, client, version string
	if err := share.db.QueryRow(`SELECT platform,client,client_version FROM devices WHERE id=?`, first.DeviceID).Scan(&platform, &client, &version); err != nil {
		t.Fatalf("query device metadata: %v", err)
	}
	if platform != "unknown" || client != "Clash Verge" || version != "2.1.3" {
		t.Fatalf("device metadata=(%q,%q,%q), want (unknown,Clash Verge,2.1.3)", platform, client, version)
	}

	// 同客户端、同来源地址（即使版本变化）：复用既有设备，不再触发同步。
	second, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.2.0", "198.51.100.10")
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if second.DeviceID != first.DeviceID || recorder.calls != 1 {
		t.Fatalf("same client and IP must reuse the device: first=%s second=%s calls=%d", first.DeviceID, second.DeviceID, recorder.calls)
	}

	// 同客户端、不同来源地址：名下仅一台时视为同一台换网络——原地更新
	// 来源地址与设备名，不新建孤儿设备，也不触发内核同步。
	otherIP, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.2.0", "203.0.113.10")
	if err != nil {
		t.Fatalf("different IP fetch: %v", err)
	}
	if otherIP.DeviceID != first.DeviceID || recorder.calls != 1 {
		t.Fatalf("single device on another IP must be reused in place: first=%s second=%s calls=%d", first.DeviceID, otherIP.DeviceID, recorder.calls)
	}
	var movedIP string
	if err := share.db.QueryRow(`SELECT source_ip FROM devices WHERE id=?`, first.DeviceID).Scan(&movedIP); err != nil || movedIP != "203.0.113.10" {
		t.Fatalf("source_ip=%q err=%v, want in-place update to 203.0.113.10", movedIP, err)
	}

	// 不同客户端 → 第二台自动设备。
	third, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "Stash/2.8", "192.0.2.10")
	if err != nil {
		t.Fatalf("third fetch: %v", err)
	}
	if third.DeviceID == first.DeviceID || recorder.calls != 2 {
		t.Fatalf("new client must create another device: %+v calls=%d", third, recorder.calls)
	}

	// 同客户端双出口（名下已两台）后再换 IP：无法判断是哪台换网，保持新建。
	if _, err := share.db.Exec(`UPDATE devices SET ua_key='clashverge' WHERE ua_key='stash'`); err != nil {
		t.Fatalf("simulate dual-exit client: %v", err)
	}
	dualExit, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.2.0", "198.51.100.77")
	if err != nil {
		t.Fatalf("dual-exit fetch: %v", err)
	}
	if dualExit.DeviceID == first.DeviceID || dualExit.DeviceID == third.DeviceID || recorder.calls != 3 {
		t.Fatalf("dual-exit client on a new IP must create a new device: first=%s third=%s got=%s calls=%d", first.DeviceID, third.DeviceID, dualExit.DeviceID, recorder.calls)
	}

	// 空 UA：回退该用户任一已有产物（无默认设备概念）。
	direct, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "")
	if err != nil {
		t.Fatalf("empty UA fetch: %v", err)
	}
	if direct.DeviceID == "" {
		t.Fatalf("empty UA must fall back to any existing artifact: %+v", direct)
	}
	devices, err := users.ListDevices(t.Context(), userID)
	if err != nil || len(devices) != 3 {
		t.Fatalf("devices=%+v err=%v, want 3 (no default device)", devices, err)
	}
}

func TestAutoDiscoveryDirectDeviceToken(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID
	auto, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3")
	if err != nil {
		t.Fatalf("auto fetch: %v", err)
	}
	// 用别的 UA 拉自动设备的 token：直连该设备，不受 UA 影响。
	devices, _ := users.ListDevices(t.Context(), userID)
	var autoToken string
	for _, device := range devices {
		if device.ID == auto.DeviceID {
			autoToken = device.Token
		}
	}
	if autoToken == "" {
		t.Fatal("auto device token not found")
	}
	// 直连拉取也要刷新设备元数据：换来源地址后（绕过节流）客户端观察值
	// （Stash）与新 IP 应写回该设备。
	direct, err := share.ArtifactByToken(t.Context(), autoToken, "clash.yaml", "Stash/2.8", "203.0.113.50")
	if err != nil {
		t.Fatalf("direct fetch: %v", err)
	}
	if direct.DeviceID != auto.DeviceID {
		t.Fatalf("device token must route directly: %+v", direct)
	}
	devices, _ = users.ListDevices(t.Context(), userID)
	for _, device := range devices {
		if device.ID != auto.DeviceID {
			continue
		}
		if device.Client != "Stash" || device.SourceIP != "203.0.113.50" || device.LastSeenAt == nil {
			t.Fatalf("direct fetch should refresh metadata: client=%q sourceIp=%q lastSeen=%v", device.Client, device.SourceIP, device.LastSeenAt)
		}
	}
}

func TestAutoDiscoveryCapFallsBackToExistingArtifact(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID
	// 先正常自动发现一台设备（产物就位），再手动填满设备上限。
	if _, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3"); err != nil {
		t.Fatalf("seed auto device: %v", err)
	}
	for i := 0; i < serviceuser.MaxDevicesPerUser-1; i++ {
		if _, created, found, err := users.DeviceForKey(t.Context(), userID, fmt.Sprintf("manual-%d", i), fmt.Sprintf("Client %d", i), serviceuser.DeviceMetadata{SourceIP: fmt.Sprintf("198.51.100.%d", i+1)}); err != nil || !created || !found {
			t.Fatalf("fill devices: created=%v found=%v err=%v", created, found, err)
		}
	}
	calls := recorder.calls
	fallback, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "Stash/2.8")
	if err != nil {
		t.Fatalf("capped fetch: %v", err)
	}
	if fallback.DeviceID == "" || recorder.calls != calls {
		t.Fatalf("cap must fall back to an existing artifact without sync: %+v calls delta=%d", fallback, recorder.calls-calls)
	}
}

func TestDisabledAutoDevice(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID
	if _, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3"); err != nil {
		t.Fatalf("auto fetch: %v", err)
	}
	devices, _ := users.ListDevices(t.Context(), userID)
	for _, device := range devices {
		if device.Auto {
			if err := users.SetDeviceEnabled(t.Context(), userID, device.ID, false); err != nil {
				t.Fatalf("disable: %v", err)
			}
		}
	}
	// 停用的自动设备：同 UA 再拉取返回占位错误。
	if _, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3"); !errors.Is(err, ErrDeviceDisabled) {
		t.Fatalf("err=%v, want ErrDeviceDisabled", err)
	}
	// 空 UA：回退任意已有产物（此处只有已停用设备，仍可服务）。
	if _, err := share.ArtifactByToken(context.Background(), token, "clash.yaml", ""); err != nil {
		t.Fatalf("fallback fetch after disable: %v", err)
	}
}
