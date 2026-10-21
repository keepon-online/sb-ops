package user

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/keepon-online/sb-ops/internal/database"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(database.Options{Path: filepath.Join(t.TempDir(), "control.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewStore(db)
}

func TestCreateIssuesUserTokenWithoutDevices(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)

	created, token, err := svc.Create(t.Context(), CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if token == "" {
		t.Fatal("create must issue a user-level subscription token")
	}
	if created.DeviceCount != 0 {
		t.Fatalf("deviceCount=%d want 0 (devices appear on client import)", created.DeviceCount)
	}
	devices, err := svc.ListDevices(t.Context(), created.ID)
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices=%+v err=%v, want none at creation", devices, err)
	}
	// 惰性签发/读取用户 token：二次读取稳定。
	again, err := svc.SubscriptionToken(t.Context(), created.ID)
	if err != nil || again != token {
		t.Fatalf("subscription token=%q err=%v want %q", again, err, token)
	}
	rotated, err := svc.RotateSubscriptionToken(t.Context(), created.ID)
	if err != nil || rotated == token || rotated == "" {
		t.Fatalf("rotate token=%q err=%v", rotated, err)
	}
	kept, err := svc.SubscriptionToken(t.Context(), created.ID)
	if err != nil || kept != rotated {
		t.Fatalf("token after rotate=%q err=%v want %q", kept, err, rotated)
	}
}

func TestDeviceLifecycle(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 订阅自动发现设备并拿到独立凭据。
	phone, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"})
	if err != nil || !createdNew || !found {
		t.Fatalf("discover device: %+v created=%v found=%v err=%v", phone, createdNew, found, err)
	}
	phoneItems, err := svc.ListDevices(ctx, created.ID)
	if err != nil || len(phoneItems) != 1 {
		t.Fatalf("devices=%+v err=%v want 1", phoneItems, err)
	}
	if phoneItems[0].Token == "" {
		t.Fatalf("discovered device should expose token in list: %+v", phoneItems)
	}
	phone.Token = phoneItems[0].Token

	// 轮换设备：token 更换、设备 id 与名称保持。
	rotated, rotatedIssued, err := svc.RotateDevice(ctx, created.ID, phone.ID)
	if err != nil {
		t.Fatalf("rotate device: %v", err)
	}
	if rotated.Token != rotatedIssued.Token || rotated.Token == phone.Token || rotated.Name != "Clash Verge · 198.51.100.10" || rotated.ID != phone.ID {
		t.Fatalf("rotated=%+v issued=%+v", rotated, rotatedIssued)
	}

	// 停用与启用。
	if err := svc.SetDeviceEnabled(ctx, created.ID, phone.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	devices, err := svc.ListDevices(ctx, created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, device := range devices {
		if device.ID == phone.ID && device.Enabled {
			t.Fatal("device should be disabled")
		}
	}

	// 删除设备。
	if err := svc.DeleteDevice(ctx, created.ID, phone.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	devices, err = svc.ListDevices(ctx, created.ID)
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices=%+v err=%v want 0 after delete", devices, err)
	}
	if _, _, err := svc.RotateDevice(ctx, created.ID, phone.ID); err != common.ErrNotFound {
		t.Fatalf("rotate deleted device err=%v want ErrNotFound", err)
	}
}

func TestAutoDiscoveryDeviceCap(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := range MaxDevicesPerUser {
		_, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, fmt.Sprintf("client-%d", i), fmt.Sprintf("Client %d", i), DeviceMetadata{SourceIP: fmt.Sprintf("198.51.100.%d", i+1)})
		if err != nil || !createdNew || !found {
			t.Fatalf("discover device %d: created=%v found=%v err=%v", i, createdNew, found, err)
		}
	}
	_, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "overflow", "Overflow", DeviceMetadata{SourceIP: "203.0.113.1"})
	if err != nil || createdNew || found {
		t.Fatalf("device count must cap at %d: created=%v found=%v err=%v", MaxDevicesPerUser, createdNew, found, err)
	}
}

func TestDeviceUserMap(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	phone, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"})
	if err != nil || !createdNew || !found {
		t.Fatalf("discover device: %+v created=%v found=%v err=%v", phone, createdNew, found, err)
	}
	owned, err := store.DeviceUserMap(ctx)
	if err != nil {
		t.Fatalf("device user map: %v", err)
	}
	for deviceID := range owned {
		if owned[deviceID] != created.ID {
			t.Fatalf("device %s should map to user %s, got %s", deviceID, created.ID, owned[deviceID])
		}
	}
	if _, ok := owned[phone.ID]; !ok {
		t.Fatal("phone device missing from map")
	}
}

func TestAdjustQuotaAddAndSet(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	created, _, err := svc.Create(t.Context(), CreateInput{Name: "quota-user", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := svc.AdjustQuota(t.Context(), created.ID, TrafficInput{Mode: "add", Bytes: 2 << 30})
	if err != nil {
		t.Fatalf("add quota: %v", err)
	}
	if updated.QuotaBytes != 2<<30 {
		t.Fatalf("quota=%d, want %d", updated.QuotaBytes, 2<<30)
	}

	updated, err = svc.AdjustQuota(t.Context(), created.ID, TrafficInput{Mode: "set", Bytes: 1 << 30})
	if err != nil {
		t.Fatalf("set quota: %v", err)
	}
	if updated.QuotaBytes != 1<<30 {
		t.Fatalf("quota=%d, want %d", updated.QuotaBytes, 1<<30)
	}

	// 减到负数必须被拒绝且保持原值
	if _, err := svc.AdjustQuota(t.Context(), created.ID, TrafficInput{Mode: "add", Bytes: -(2 << 30)}); err == nil {
		t.Fatal("expected error when quota would go negative")
	}
	kept, err := svc.Get(t.Context(), created.ID)
	if err != nil || kept.QuotaBytes != 1<<30 {
		t.Fatalf("quota=%d err=%v, want unchanged %d", kept.QuotaBytes, err, 1<<30)
	}
}

func TestListReturnsUsersInOneBatch(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	base := time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { base = base.Add(time.Minute); return base }
	for _, name := range []string{"alice", "bob", "carol"} {
		if _, _, err := svc.Create(ctx, CreateInput{Name: name, Enabled: true}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	page, err := store.List(ctx, ListInput{Limit: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items=%d want 3", len(page.Items))
	}
	if page.NextCursor != "" {
		t.Fatalf("nextCursor=%q want empty", page.NextCursor)
	}
	// 默认按 created_at DESC 排序，最后创建的在最前。
	if page.Items[0].Name != "carol" || page.Items[1].Name != "bob" || page.Items[2].Name != "alice" {
		t.Fatalf("order=%v", []string{page.Items[0].Name, page.Items[1].Name, page.Items[2].Name})
	}

	limited, err := store.List(ctx, ListInput{Limit: 2})
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited.Items) != 2 || limited.NextCursor == "" {
		t.Fatalf("limited items=%d cursor=%q want 2 items and a cursor", len(limited.Items), limited.NextCursor)
	}
}

func TestAutoDiscoverySingleDeviceIPChangeUpdatesInPlace(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"})
	if err != nil || !createdNew || !found {
		t.Fatalf("first discover: created=%v found=%v err=%v", createdNew, found, err)
	}
	// 换网络重拉：同一客户端名下仅一台 → 原地更新 IP 与名字，不新建孤儿设备。
	moved, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "203.0.113.99"})
	if err != nil || createdNew || !found {
		t.Fatalf("ip change should reuse the single device: created=%v found=%v err=%v", createdNew, found, err)
	}
	if moved.ID != first.ID || moved.Name != "Clash Verge · 203.0.113.99" {
		t.Fatalf("moved=%+v want id=%s and name with the new ip", moved, first.ID)
	}
	devices, err := svc.ListDevices(ctx, created.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%d err=%v want 1", len(devices), err)
	}
	if devices[0].SourceIP != "203.0.113.99" {
		t.Fatalf("sourceIp=%q want 203.0.113.99", devices[0].SourceIP)
	}
	// 同一客户端第二台（同客户端双出口的历史数据）后再换 IP：无法判断是哪台
	// 换网，应保持新建而不是覆盖既有设备。
	if _, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "singbox", "sing-box", DeviceMetadata{SourceIP: "192.0.2.1"}); err != nil || !createdNew || !found {
		t.Fatalf("second client: created=%v found=%v err=%v", createdNew, found, err)
	}
	if _, err := store.db.Exec(`UPDATE devices SET ua_key='clashverge' WHERE ua_key='singbox'`); err != nil {
		t.Fatalf("simulate dual-exit clashverge: %v", err)
	}
	third, createdNew, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.77"})
	if err != nil || !createdNew || !found {
		t.Fatalf("dual-exit new ip must create a new device: created=%v found=%v err=%v", createdNew, found, err)
	}
	if third.ID == first.ID {
		t.Fatal("dual-exit must not overwrite the existing device")
	}
	devices, err = svc.ListDevices(ctx, created.ID)
	if err != nil || len(devices) != 3 {
		t.Fatalf("devices=%d err=%v want 3", len(devices), err)
	}
}

func TestUpdateDevicePlatformOnlyFillsUnknown(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	unknownDevice, _, _, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "203.0.113.9"})
	if err != nil {
		t.Fatalf("discover unknown device: %v", err)
	}
	knownDevice, _, _, err := svc.DeviceForKey(ctx, created.ID, "stash", "Stash", DeviceMetadata{SourceIP: "203.0.113.10"})
	if err != nil {
		t.Fatalf("discover known device: %v", err)
	}
	refs, err := svc.UnknownPlatformDevices(ctx)
	if err != nil || len(refs) != 2 {
		t.Fatalf("unknown refs=%+v err=%v want 2 (both unknown initially)", refs, err)
	}
	if err := svc.UpdateDevicePlatform(ctx, created.ID, knownDevice.ID, "macos"); err != nil {
		t.Fatalf("seed known platform: %v", err)
	}
	if err := svc.UpdateDevicePlatform(ctx, created.ID, unknownDevice.ID, "windows"); err != nil {
		t.Fatalf("infer platform: %v", err)
	}
	// 再推一次不得覆盖已识别的值。
	if err := svc.UpdateDevicePlatform(ctx, created.ID, unknownDevice.ID, "android"); err != nil {
		t.Fatalf("second infer: %v", err)
	}
	devices, err := svc.ListDevices(ctx, created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	platforms := map[string]string{}
	for _, device := range devices {
		platforms[device.ID] = device.Platform
	}
	if platforms[unknownDevice.ID] != "windows" || platforms[knownDevice.ID] != "macos" {
		t.Fatalf("platforms=%v, want windows stays windows after android push and macos untouched", platforms)
	}
	if refs, _ = svc.UnknownPlatformDevices(ctx); len(refs) != 0 {
		t.Fatalf("unknown refs after inference=%+v, want 0", refs)
	}
}

// TestUpdateDeviceMetadataKeepsInferredPlatform 回归订阅刷新覆盖推断平台的 Bug：
// Clash Verge 等 UA 解析平台为 unknown，订阅周期刷新不得把 DeviceProfiler
// 已推断的 windows/macos 抹回 unknown；确定性的平台仍应正常写入。
func TestUpdateDeviceMetadataKeepsInferredPlatform(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	device, _, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "203.0.113.9"})
	if err != nil || !found {
		t.Fatalf("discover device found=%v err=%v", found, err)
	}
	if err := svc.UpdateDevicePlatform(ctx, created.ID, device.ID, "windows"); err != nil {
		t.Fatalf("infer platform: %v", err)
	}
	now := time.Now().UTC()
	md := DeviceMetadata{Platform: "unknown", Client: "Clash Verge", ClientVersion: "2.1.3", SourceIP: "203.0.113.9", LastSeenAt: &now}
	if err := svc.UpdateDeviceMetadata(ctx, created.ID, device.ID, md); err != nil {
		t.Fatalf("refresh with unknown platform: %v", err)
	}
	devices, err := svc.ListDevices(ctx, created.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 || devices[0].Platform != "windows" {
		t.Fatalf("platform after unknown refresh=%q, want windows (inferred platform must persist)", devices[0].Platform)
	}
	// 确定性的平台（UA 携带）应能覆盖推断值；用晚于换取间隔的时间戳绕过 5 分钟节流。
	after := now.Add(6 * time.Minute)
	md.Platform = "android"
	md.LastSeenAt = &after
	if err := svc.UpdateDeviceMetadata(ctx, created.ID, device.ID, md); err != nil {
		t.Fatalf("refresh with android: %v", err)
	}
	if devices, _ = svc.ListDevices(ctx, created.ID); devices[0].Platform != "android" {
		t.Fatalf("platform after android refresh=%q, want android", devices[0].Platform)
	}
}

func TestFindDeviceByIDAndClaimDevice(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dev, _, found, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"})
	if err != nil || !found {
		t.Fatalf("create device: %v", err)
	}

	// 1. FindDeviceByID
	foundDev, ok, err := svc.FindDeviceByID(ctx, created.ID, dev.ID)
	if err != nil || !ok || foundDev.ID != dev.ID {
		t.Fatalf("FindDeviceByID: dev=%+v ok=%v err=%v", foundDev, ok, err)
	}
	// 不存在的设备 ID
	if _, ok, err := svc.FindDeviceByID(ctx, created.ID, "nonexistent-id"); err != nil || ok {
		t.Fatalf("FindDeviceByID nonexistent: ok=%v err=%v", ok, err)
	}

	// 2. ClaimDevice 更新来源地址与名称
	claimed, ok, err := svc.ClaimDevice(ctx, created.ID, dev.ID, "Clash Verge", DeviceMetadata{SourceIP: "203.0.113.88"})
	if err != nil || !ok {
		t.Fatalf("ClaimDevice: %v", err)
	}
	devices, _ := svc.ListDevices(ctx, created.ID)
	if len(devices) != 1 || devices[0].SourceIP != "203.0.113.88" || devices[0].Name != "Clash Verge · 203.0.113.88" {
		t.Fatalf("device after claim: %+v (claimed=%+v)", devices[0], claimed)
	}
}

func insertTestProtocol(t *testing.T, store *Store, id string, port int) {
	t.Helper()
	if _, err := store.db.Exec(`INSERT INTO protocols(id,name,type,listen_port,created_at,updated_at) VALUES(?,?,'vless',?,0,0)`, id, id, port); err != nil {
		t.Fatalf("insert protocol %s: %v", id, err)
	}
}

func TestBatchQuotaAddSetAndRollback(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	first, _, err := svc.Create(t.Context(), CreateInput{Name: "batch-quota-a", Enabled: true})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	second, _, err := svc.Create(t.Context(), CreateInput{Name: "batch-quota-b", Enabled: true})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	if err := svc.BatchQuota(t.Context(), []string{first.ID, second.ID}, TrafficInput{Mode: "add", Bytes: 2 << 30}); err != nil {
		t.Fatalf("batch add: %v", err)
	}
	for _, id := range []string{first.ID, second.ID} {
		value, err := svc.Get(t.Context(), id)
		if err != nil || value.QuotaBytes != 2<<30 {
			t.Fatalf("user %s quota=%d err=%v, want %d", id, value.QuotaBytes, err, 2<<30)
		}
	}

	if err := svc.BatchQuota(t.Context(), []string{first.ID}, TrafficInput{Mode: "set", Bytes: 0}); err != nil {
		t.Fatalf("batch set: %v", err)
	}
	value, _ := svc.Get(t.Context(), first.ID)
	if value.QuotaBytes != 0 {
		t.Fatalf("set 0 should mean unlimited, got %d", value.QuotaBytes)
	}

	// 扣成负数必须整批回滚：第一个用户额度为 0（不限额），负增量只在第二个用户上越界，
	// 事务回滚后第二个用户的额度保持原值。
	err = svc.BatchQuota(t.Context(), []string{first.ID, second.ID}, TrafficInput{Mode: "add", Bytes: -(10 << 30)})
	if _, ok := err.(*common.ValidationError); !ok {
		t.Fatalf("want validation error, got %v", err)
	}
	kept, err := svc.Get(t.Context(), second.ID)
	if err != nil || kept.QuotaBytes != 2<<30 {
		t.Fatalf("rollback check: quota=%d err=%v, want unchanged %d", kept.QuotaBytes, err, 2<<30)
	}

	if err := svc.BatchQuota(t.Context(), nil, TrafficInput{Mode: "add", Bytes: 1}); err == nil {
		t.Fatal("empty ids must be rejected")
	}
	if err := svc.BatchQuota(t.Context(), []string{"missing"}, TrafficInput{Mode: "add", Bytes: 1}); err != common.ErrNotFound {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestBatchProtocolsModes(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	first, _, err := svc.Create(t.Context(), CreateInput{Name: "batch-proto-a", Enabled: true})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	second, _, err := svc.Create(t.Context(), CreateInput{Name: "batch-proto-b", Enabled: true})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	insertTestProtocol(t, store, "proto-1", 4443)
	insertTestProtocol(t, store, "proto-2", 8443)

	if err := svc.BatchProtocols(t.Context(), []string{first.ID, second.ID}, "add", []string{"proto-1", "proto-2"}); err != nil {
		t.Fatalf("batch add: %v", err)
	}
	value, _ := svc.Get(t.Context(), first.ID)
	if len(value.ProtocolIDs) != 2 {
		t.Fatalf("protocols=%v, want both", value.ProtocolIDs)
	}
	revisionAfterAdd := value.Revision

	// add 幂等：重复追加不报错、不产生重复行。
	if err := svc.BatchProtocols(t.Context(), []string{first.ID}, "add", []string{"proto-1"}); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	value, _ = svc.Get(t.Context(), first.ID)
	if len(value.ProtocolIDs) != 2 {
		t.Fatalf("duplicate add produced %v", value.ProtocolIDs)
	}

	if err := svc.BatchProtocols(t.Context(), []string{first.ID}, "remove", []string{"proto-1"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	value, _ = svc.Get(t.Context(), first.ID)
	if len(value.ProtocolIDs) != 1 || value.ProtocolIDs[0] != "proto-2" {
		t.Fatalf("after remove: %v", value.ProtocolIDs)
	}

	// set 覆盖：两用户同时收敛为 proto-1，且用户 revision 递增（订阅产物感知变化）。
	if err := svc.BatchProtocols(t.Context(), []string{first.ID, second.ID}, "set", []string{"proto-1"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, _ = svc.Get(t.Context(), first.ID)
	if len(value.ProtocolIDs) != 1 || value.ProtocolIDs[0] != "proto-1" || value.Revision <= revisionAfterAdd {
		t.Fatalf("after set: protocols=%v revision=%d", value.ProtocolIDs, value.Revision)
	}

	// set 空列表 = 清空。
	if err := svc.BatchProtocols(t.Context(), []string{second.ID}, "set", nil); err != nil {
		t.Fatalf("set empty: %v", err)
	}
	value, _ = svc.Get(t.Context(), second.ID)
	if len(value.ProtocolIDs) != 0 {
		t.Fatalf("set empty should clear, got %v", value.ProtocolIDs)
	}

	err = svc.BatchProtocols(t.Context(), []string{first.ID}, "add", []string{"missing-proto"})
	if _, ok := err.(*common.ValidationError); !ok {
		t.Fatalf("unknown protocol: %v", err)
	}
	if err := svc.BatchProtocols(t.Context(), []string{"missing-user"}, "add", []string{"proto-1"}); err != common.ErrNotFound {
		t.Fatalf("unknown user: %v", err)
	}
	if err := svc.BatchProtocols(t.Context(), []string{first.ID}, "merge", nil); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
}

func TestManualDeviceAndRename(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	user, _, err := svc.Create(ctx, CreateInput{Name: "bob", Enabled: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// 1. 手动添加设备：ua_key 为空，Auto 为 false，生成独立凭据。
	dev, cred, err := svc.CreateDevice(ctx, user.ID, "书房 · Verge")
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if dev.Name != "书房 · Verge" || dev.Auto != false || !dev.Enabled || dev.Token == "" || cred.Token == "" {
		t.Fatalf("invalid created device: %+v, cred: %+v", dev, cred)
	}
	if dev.Token != cred.Token || dev.UUID != cred.UUID {
		t.Fatalf("token mismatch: dev=%q cred=%q", dev.Token, cred.Token)
	}

	// 2. 空名称校验。
	if _, _, err := svc.CreateDevice(ctx, user.ID, "   "); err == nil {
		t.Fatal("empty device name should fail")
	}

	// 3. 重复名称冲突 UNIQUE(user_id, name)。
	if _, _, err := svc.CreateDevice(ctx, user.ID, "书房 · Verge"); err != common.ErrDuplicate {
		t.Fatalf("duplicate device name should return ErrDuplicate, got %v", err)
	}

	// 4. 重命名设备与改状态。
	newName := "客厅 · Verge"
	disabled := false
	updated, err := svc.UpdateDevice(ctx, user.ID, dev.ID, &newName, &disabled)
	if err != nil {
		t.Fatalf("update device: %v", err)
	}
	if updated.Name != "客厅 · Verge" || updated.Enabled != false || updated.Auto != false {
		t.Fatalf("unexpected updated device: %+v", updated)
	}

	// 5. 校验 FindDeviceByID 能正确反映 Auto=false 与更新后的属性。
	found, ok, err := svc.FindDeviceByID(ctx, user.ID, dev.ID)
	if err != nil || !ok {
		t.Fatalf("find device: ok=%v err=%v", ok, err)
	}
	if found.Name != "客厅 · Verge" || found.Enabled != false || found.Auto != false {
		t.Fatalf("found mismatch: %+v", found)
	}

	// 6. 达到上限（MaxDevicesPerUser = 10）校验。
	for i := 2; i <= MaxDevicesPerUser; i++ {
		_, _, err := svc.CreateDevice(ctx, user.ID, fmt.Sprintf("设备 %d", i))
		if err != nil {
			t.Fatalf("create device %d: %v", i, err)
		}
	}
	if _, _, err := svc.CreateDevice(ctx, user.ID, "超出上限设备"); err == nil {
		t.Fatal("creating device exceeding MaxDevicesPerUser should fail")
	}
}

func TestClaimDevicePreservesManualName(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store)
	ctx := t.Context()
	user, _, err := svc.Create(ctx, CreateInput{Name: "carol", Enabled: true})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	manual, _, err := svc.CreateDevice(ctx, user.ID, "书房 · Verge")
	if err != nil {
		t.Fatalf("create manual device: %v", err)
	}

	// ETag 锚定把用户级链接的拉取认领到手动行：名称保持，元数据回填。
	claimed, ok, err := svc.ClaimDevice(ctx, user.ID, manual.ID, "Clash Verge", DeviceMetadata{
		Platform: "unknown", Client: "Clash Verge", ClientVersion: "2.5.5", SourceIP: "203.0.113.10",
	})
	if err != nil || !ok {
		t.Fatalf("claim manual device: ok=%v err=%v", ok, err)
	}
	if claimed.Name != "书房 · Verge" {
		t.Fatalf("manual device name must be preserved, got %q", claimed.Name)
	}
	devices, err := svc.ListDevices(ctx, user.ID)
	if err != nil || len(devices) != 1 {
		t.Fatalf("list devices: err=%v count=%d", err, len(devices))
	}
	if devices[0].Client != "Clash Verge" || devices[0].ClientVersion != "2.5.5" || devices[0].SourceIP != "203.0.113.10" {
		t.Fatalf("claim must still backfill metadata: %+v", devices[0])
	}

	// 对照：自动发现设备被认领时仍按新来源地址自动改名。
	auto, _, found, err := svc.DeviceForKey(ctx, user.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"})
	if err != nil || !found {
		t.Fatalf("auto device: found=%v err=%v", found, err)
	}
	roamed, ok, err := svc.ClaimDevice(ctx, user.ID, auto.ID, "Clash Verge", DeviceMetadata{SourceIP: "203.0.113.99"})
	if err != nil || !ok {
		t.Fatalf("claim auto device: ok=%v err=%v", ok, err)
	}
	if roamed.Name != "Clash Verge · 203.0.113.99" {
		t.Fatalf("auto device should be renamed on claim, got %q", roamed.Name)
	}
}
