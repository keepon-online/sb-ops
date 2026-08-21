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
	for i := 0; i < MaxDevicesPerUser; i++ {
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
