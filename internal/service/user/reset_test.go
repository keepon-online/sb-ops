package user

import (
	"testing"
	"time"
)

// fixedResetClock 返回一个可推进的测试时钟：svc.now 读取 current 的最新值。
func fixedResetClock(t *testing.T, start time.Time) (*Service, *time.Time) {
	t.Helper()
	store := newTestStore(t)
	svc := NewService(store)
	current := start
	svc.now = func() time.Time { return current }
	return svc, &current
}

func TestResetDueQuotasZeroesUserAndDevices(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc, clock := fixedResetClock(t, start)
	ctx := t.Context()

	created, _, err := svc.Create(ctx, CreateInput{Name: "alice", Enabled: true, QuotaBytes: 1000, ResetIntervalDays: 30})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, _, err := svc.DeviceForKey(ctx, created.ID, "clashverge", "Clash Verge", DeviceMetadata{SourceIP: "198.51.100.10"}); err != nil {
		t.Fatalf("discover device: %v", err)
	}
	if _, err := svc.AdjustTraffic(ctx, created.ID, TrafficInput{Mode: "add", Bytes: 500}); err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	// 边界（创建+30d）已过 1 天：重置并推进到下一边界（创建+60d）。
	*clock = start.AddDate(0, 0, 31)
	result, err := svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset due quotas: %v", err)
	}
	if len(result.Reset) != 1 || len(result.Recovered) != 0 {
		t.Fatalf("reset=%d recovered=%d, want 1/0 (500B 用量未超额)", len(result.Reset), len(result.Recovered))
	}
	if result.Reset[0].Before.UsedBytes != 500 {
		t.Fatalf("before used=%d want 500", result.Reset[0].Before.UsedBytes)
	}
	wantNext := start.AddDate(0, 0, 60)
	if result.Reset[0].NextResetAt == nil || !result.Reset[0].NextResetAt.Equal(wantNext) {
		t.Fatalf("nextResetAt=%v want %v", result.Reset[0].NextResetAt, wantNext)
	}
	after, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.UsedBytes != 0 || after.NextResetAt == nil || !after.NextResetAt.Equal(wantNext) {
		t.Fatalf("after reset used=%d next=%v, want 0/%v", after.UsedBytes, after.NextResetAt, wantNext)
	}
	if after.Revision <= created.Revision {
		t.Fatalf("revision=%d must increase beyond %d", after.Revision, created.Revision)
	}
	if after.Status != StatusActive {
		t.Fatalf("status=%q want active", after.Status)
	}
	devices, err := svc.ListDevices(ctx, created.ID)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	for _, device := range devices {
		if device.UsedBytes != 0 {
			t.Fatalf("device %s used=%d want 0", device.ID, device.UsedBytes)
		}
	}
}

func TestResetDueQuotasRecoversOnlyAvailableExceededUsers(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc, clock := fixedResetClock(t, start)
	ctx := t.Context()
	store := svc.store

	exceeded, _, err := svc.Create(ctx, CreateInput{Name: "exceeded", Enabled: true, QuotaBytes: 100, ResetIntervalDays: 30})
	if err != nil {
		t.Fatalf("create exceeded: %v", err)
	}
	disabled, _, err := svc.Create(ctx, CreateInput{Name: "disabled", Enabled: true, QuotaBytes: 100, ResetIntervalDays: 30})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	expired, _, err := svc.Create(ctx, CreateInput{Name: "expired", Enabled: true, QuotaBytes: 100, ResetIntervalDays: 30})
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	for _, id := range []string{exceeded.ID, disabled.ID, expired.ID} {
		if _, err := svc.AdjustTraffic(ctx, id, TrafficInput{Mode: "add", Bytes: 100}); err != nil {
			t.Fatalf("add traffic %s: %v", id, err)
		}
	}
	deadline := start.AddDate(0, 0, 30)
	if err := store.SetStatus(ctx, []string{disabled.ID}, false, start); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE users SET expires_at=? WHERE id=?`, deadline.Unix(), expired.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}

	*clock = start.AddDate(0, 0, 31)
	result, err := svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset due quotas: %v", err)
	}
	if len(result.Reset) != 3 {
		t.Fatalf("reset=%d want 3", len(result.Reset))
	}
	if len(result.Recovered) != 1 || result.Recovered[0].ID != exceeded.ID {
		t.Fatalf("recovered=%+v, want only exceeded user %s", result.Recovered, exceeded.ID)
	}
	// 停用/到期用户同样被清零，只是不进恢复名单。
	for _, id := range []string{disabled.ID, expired.ID} {
		value, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if value.UsedBytes != 0 {
			t.Fatalf("%s used=%d want 0", id, value.UsedBytes)
		}
	}
}

func TestResetDueQuotasSkipsNotDueAndNoInterval(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc, clock := fixedResetClock(t, start)
	ctx := t.Context()

	due, _, err := svc.Create(ctx, CreateInput{Name: "due", Enabled: true, QuotaBytes: 1000, ResetIntervalDays: 30})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	never, _, err := svc.Create(ctx, CreateInput{Name: "never", Enabled: true, QuotaBytes: 1000})
	if err != nil {
		t.Fatalf("create never: %v", err)
	}
	for _, id := range []string{due.ID, never.ID} {
		if _, err := svc.AdjustTraffic(ctx, id, TrafficInput{Mode: "add", Bytes: 100}); err != nil {
			t.Fatalf("add traffic %s: %v", id, err)
		}
	}

	// 边界未到（+10d < +30d）、无周期用户的 next_reset_at 为 NULL：都不动。
	*clock = start.AddDate(0, 0, 10)
	result, err := svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset due quotas: %v", err)
	}
	if len(result.Reset) != 0 {
		t.Fatalf("reset=%d want 0", len(result.Reset))
	}
	for _, id := range []string{due.ID, never.ID} {
		value, err := svc.Get(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if value.UsedBytes != 100 {
			t.Fatalf("%s used=%d want 100 (未到期不得清零)", id, value.UsedBytes)
		}
	}
}

func TestResetDueQuotasCatchUpAcrossIntervals(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc, clock := fixedResetClock(t, start)
	ctx := t.Context()

	created, _, err := svc.Create(ctx, CreateInput{Name: "catchup", Enabled: true, QuotaBytes: 1000, ResetIntervalDays: 10})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.AdjustTraffic(ctx, created.ID, TrafficInput{Mode: "add", Bytes: 300}); err != nil {
		t.Fatalf("add traffic: %v", err)
	}

	// 停机跨过 3 个边界（+10/+20/+30d）：一次清零，边界对齐到 +40d。
	*clock = start.AddDate(0, 0, 35)
	result, err := svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset due quotas: %v", err)
	}
	if len(result.Reset) != 1 {
		t.Fatalf("reset=%d want 1", len(result.Reset))
	}
	wantNext := start.AddDate(0, 0, 40)
	if result.Reset[0].NextResetAt == nil || !result.Reset[0].NextResetAt.Equal(wantNext) {
		t.Fatalf("nextResetAt=%v want %v", result.Reset[0].NextResetAt, wantNext)
	}
	after, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.UsedBytes != 0 {
		t.Fatalf("used=%d want 0", after.UsedBytes)
	}

	// 再到点前重复调用不动它。
	*clock = start.AddDate(0, 0, 39)
	result, err = svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset again: %v", err)
	}
	if len(result.Reset) != 0 {
		t.Fatalf("reset=%d want 0 before boundary", len(result.Reset))
	}
}

func TestResetDueQuotasHealsIntervalZeroRow(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	svc, clock := fixedResetClock(t, start)
	ctx := t.Context()

	created, _, err := svc.Create(ctx, CreateInput{Name: "dirty", Enabled: true, QuotaBytes: 1000})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 正常路径不会产出这种行（Update 改 interval=0 会同步清 next_reset_at），
	// 只有手改数据/历史遗留会出现：interval=0 但 next_reset_at 残留且已过期。
	if _, err := svc.store.db.ExecContext(ctx, `UPDATE users SET reset_interval_days=0,next_reset_at=? WHERE id=?`, start.AddDate(0, 0, -1).Unix(), created.ID); err != nil {
		t.Fatalf("seed dirty row: %v", err)
	}

	*clock = start
	result, err := svc.ResetDueQuotas(ctx)
	if err != nil {
		t.Fatalf("reset due quotas: %v", err)
	}
	if len(result.Reset) != 1 {
		t.Fatalf("reset=%d want 1", len(result.Reset))
	}
	if result.Reset[0].NextResetAt != nil {
		t.Fatalf("nextResetAt=%v want nil (interval=0 自愈为不再周期重置)", result.Reset[0].NextResetAt)
	}
	after, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.UsedBytes != 0 || after.NextResetAt != nil || after.ResetIntervalDays != 0 {
		t.Fatalf("after heal used=%d next=%v interval=%d, want 0/nil/0", after.UsedBytes, after.NextResetAt, after.ResetIntervalDays)
	}
}
