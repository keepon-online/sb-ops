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

func TestParseUserAgentKeyLabel(t *testing.T) {
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
		// 浏览器 UA 不自动建设备：Key 留空，Label 留空。
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "", ""},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "", ""},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0", "", ""},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0", "", ""},
	}
	for _, tc := range cases {
		info := ParseUserAgent(tc.ua)
		if info.Key != tc.key || info.Label != tc.label {
			t.Fatalf("UA %q → (%q,%q), want (%q,%q)", tc.ua, info.Key, info.Label, tc.key, tc.label)
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
		// 浏览器 UA：不建设备（Key 空），但平台仍按 UA 识别。
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36", "", "", "windows", ""},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "", "", "macos", ""},
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

// TestSelfLoopFetchKeepsDeviceSourceIP 覆盖 TUN/系统代理下的自环拉取：订阅
// 更新走隧道、由同进程内核回连面板，来源地址是本机地址——不得冲掉设备已有
// 的真实出口地址与名字，也不得误建新设备；首次即自环的拉取不落来源地址，
// 后续直连拉取原地认领。
func TestSelfLoopFetchKeepsDeviceSourceIP(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID

	first, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.1.3", "198.51.100.10")
	if err != nil {
		t.Fatalf("direct fetch: %v", err)
	}
	selfFetch, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "clash-verge/2.2.0", "127.0.0.1")
	if err != nil {
		t.Fatalf("self-loop fetch: %v", err)
	}
	if selfFetch.DeviceID != first.DeviceID || recorder.calls != 1 {
		t.Fatalf("self-loop fetch must reuse the device: first=%s got=%s calls=%d", first.DeviceID, selfFetch.DeviceID, recorder.calls)
	}
	var sourceIP, name string
	if err := share.db.QueryRow(`SELECT source_ip,name FROM devices WHERE id=?`, first.DeviceID).Scan(&sourceIP, &name); err != nil || sourceIP != "198.51.100.10" || name != "Clash Verge · 198.51.100.10" {
		t.Fatalf("device=(%q,%q) err=%v, want source_ip and name unchanged", sourceIP, name, err)
	}

	// 首次拉取即自环（新客户端）：不写入本机地址；下次直连拉取原地认领并
	// 回填真实地址与名字。
	loopFirst, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "Stash/2.8", "127.0.0.1")
	if err != nil {
		t.Fatalf("loop-first fetch: %v", err)
	}
	if loopFirst.DeviceID == "" {
		t.Fatalf("loop-first fetch should still serve an artifact: %+v", loopFirst)
	}
	if err := share.db.QueryRow(`SELECT source_ip,name FROM devices WHERE id=?`, loopFirst.DeviceID).Scan(&sourceIP, &name); err != nil || sourceIP != "" || name != "Stash" {
		t.Fatalf("loop-created device=(%q,%q) err=%v, want empty source_ip and bare label name", sourceIP, name, err)
	}
	claim, err := share.ArtifactByToken(t.Context(), token, "clash.yaml", "Stash/2.8", "192.0.2.5")
	if err != nil {
		t.Fatalf("claim fetch: %v", err)
	}
	if claim.DeviceID != loopFirst.DeviceID || recorder.calls != 2 {
		t.Fatalf("direct fetch must claim the loop-created device: loop=%s got=%s calls=%d", loopFirst.DeviceID, claim.DeviceID, recorder.calls)
	}
	if err := share.db.QueryRow(`SELECT source_ip,name FROM devices WHERE id=?`, loopFirst.DeviceID).Scan(&sourceIP, &name); err != nil || sourceIP != "192.0.2.5" || name != "Stash · 192.0.2.5" {
		t.Fatalf("claimed device=(%q,%q) err=%v, want in-place update to 192.0.2.5", sourceIP, name, err)
	}
	devices, err := users.ListDevices(t.Context(), userID)
	if err != nil || len(devices) != 2 {
		t.Fatalf("devices=%+v err=%v, want 2 (no self-loop duplicates)", devices, err)
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

func TestCleanETag(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{`"abc123"`, "abc123"},
		{`W/"abc123"`, "abc123"},
		{"abc123", "abc123"},
		{`  "abc123"  `, "abc123"},
		{"", ""},
		{`""`, ""},
	}
	for _, tc := range cases {
		if got := CleanETag(tc.input); got != tc.want {
			t.Fatalf("CleanETag(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestMultiDeviceRoamingWithETag 验证同 UA 客户端名下已有多台设备时，
// 漫游换网通过 ETag 精确认领旧设备，不误建新设备且不触发多余内核同步。
func TestMultiDeviceRoamingWithETag(t *testing.T) {
	share, users, recorder := newTestService(t)
	userID, token := mustCreateUser(t, users)
	recorder.userID = userID
	ctx := t.Context()

	// 1. 同一用户下有两台同一客户端（clashverge）的设备
	first, err := share.ArtifactByToken(ctx, token, "clash.yaml", "clash-verge/2.1.3", "198.51.100.10")
	if err != nil {
		t.Fatalf("discover device 1: %v", err)
	}
	secondRaw, err := share.ArtifactByToken(ctx, token, "clash.yaml", "Stash/2.8", "198.51.100.20")
	if err != nil {
		t.Fatalf("discover device 2: %v", err)
	}
	if _, err := share.db.Exec(`UPDATE devices SET ua_key='clashverge', name='Clash Verge · 198.51.100.20' WHERE id=?`, secondRaw.DeviceID); err != nil {
		t.Fatalf("set second device ua_key: %v", err)
	}
	second, err := share.ArtifactByToken(ctx, token, "clash.yaml", "clash-verge/2.1.3", "198.51.100.20")
	if err != nil {
		t.Fatalf("fetch second device: %v", err)
	}
	if first.DeviceID == second.DeviceID {
		t.Fatalf("device 1 and 2 must have distinct IDs, got both %s", first.DeviceID)
	}
	devices, err := users.ListDevices(ctx, userID)
	if err != nil || len(devices) != 2 {
		t.Fatalf("initial devices=%d want 2", len(devices))
	}
	syncCalls := recorder.calls

	// 2. 设备 2 移动到全新网络 (203.0.113.99)，携带设备 2 的 ETag 刷新订阅
	roamed, err := share.ArtifactByToken(ctx, token, "clash.yaml", "clash-verge/2.1.3", "203.0.113.99", `"`+second.Digest+`"`)
	if err != nil {
		t.Fatalf("roam device 2: %v", err)
	}
	if roamed.DeviceID != second.DeviceID {
		t.Fatalf("roamed device ID = %s, want %s (device 2)", roamed.DeviceID, second.DeviceID)
	}
	// 验证总设备数未增加（未新建第 3 台设备）且未触发内核重载
	devices, _ = users.ListDevices(ctx, userID)
	if len(devices) != 2 {
		t.Fatalf("device count after roam = %d, want 2 (no ghost device)", len(devices))
	}
	if recorder.calls != syncCalls {
		t.Fatalf("roaming must not trigger kernel sync, got calls=%d want %d", recorder.calls, syncCalls)
	}
	// 验证设备 2 的来源地址与名字已原地更新，设备 1 保持原样
	for _, d := range devices {
		switch d.ID {
		case second.DeviceID:
			if d.SourceIP != "203.0.113.99" || d.Name != "Clash Verge · 203.0.113.99" {
				t.Fatalf("device 2 not updated in place: %+v", d)
			}
		case first.DeviceID:
			if d.SourceIP != "198.51.100.10" {
				t.Fatalf("device 1 unexpectedly modified: %+v", d)
			}
		}
	}

	// 3. 设备 1 也漫游到与设备 2 相同的 IP (203.0.113.99，如同一家庭 Wi-Fi)，携带设备 1 的 ETag
	coexist, err := share.ArtifactByToken(ctx, token, "clash.yaml", "clash-verge/2.1.3", "203.0.113.99", first.Digest)
	if err != nil {
		t.Fatalf("coexist roam: %v", err)
	}
	if coexist.DeviceID != first.DeviceID {
		t.Fatalf("coexist device ID = %s, want %s (device 1)", coexist.DeviceID, first.DeviceID)
	}
	devices, _ = users.ListDevices(ctx, userID)
	if len(devices) != 2 {
		t.Fatalf("coexist device count = %d, want 2", len(devices))
	}

	// 4. 模拟配置更新：服务端重新生成产物后摘要变更，设备 2 带着历史旧 ETag 换网拉取
	oldDigest := second.Digest
	newDigest := "digest-after-server-config-update"
	if _, err := share.db.Exec(`UPDATE runtime_artifacts SET digest=? WHERE user_id=? AND device_id=?`, newDigest, userID, second.DeviceID); err != nil {
		t.Fatalf("update artifact digest: %v", err)
	}
	reclaimed, err := share.ArtifactByToken(ctx, token, "clash.yaml", "clash-verge/2.1.3", "192.0.2.88", oldDigest)
	if err != nil {
		t.Fatalf("reclaim with historical digest: %v", err)
	}
	if reclaimed.DeviceID != second.DeviceID {
		t.Fatalf("reclaimed device ID = %s, want %s (device 2 via historical digest)", reclaimed.DeviceID, second.DeviceID)
	}
	if reclaimed.Digest != newDigest {
		t.Fatalf("reclaimed digest = %s, want new digest %s", reclaimed.Digest, newDigest)
	}
	devices, _ = users.ListDevices(ctx, userID)
	if len(devices) != 2 {
		t.Fatalf("historical digest reclaim device count = %d, want 2", len(devices))
	}
}
