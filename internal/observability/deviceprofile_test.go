package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/gookit/slog"
)

func TestDomainPlatformSuffixMatching(t *testing.T) {
	cases := []struct {
		host     string
		want     string
		wantBool bool
	}{
		{"mtalk.google.com", "android", true},
		{"mobile-talk.mtalk.google.com", "android", true},
		{"notmtalk.google.com", "", false},
		{"dns.msftncsi.com", "windows", true},
		{"update.microsoft.com", "windows", true},
		{"swcdn.apple.com", "macos", true},
		{"push.apple.com", "", false}, // iOS/macOS 共用，无高置信，不判
		{"", "", false},
		{"EXAMPLE.COM", "", false},
	}
	for _, tc := range cases {
		platform, ok := domainPlatform(tc.host)
		if ok != tc.wantBool || platform != tc.want {
			t.Errorf("domainPlatform(%q)=(%q,%v), want (%q,%v)", tc.host, platform, ok, tc.want, tc.wantBool)
		}
	}
}

func TestInferPlatformThreshold(t *testing.T) {
	if platform, ok := inferPlatform(map[string]map[string]bool{"windows": {"dns.msftncsi.com": true}}); ok || platform != "" {
		t.Fatalf("single domain must not infer, got (%q,%v)", platform, ok)
	}
	platform, ok := inferPlatform(map[string]map[string]bool{
		"windows": {"dns.msftncsi.com": true, "www.msftconnecttest.com": true},
		"macos":   {"swscan.apple.com": true},
	})
	if !ok || platform != "windows" {
		t.Fatalf("two distinct domains must infer windows, got (%q,%v)", platform, ok)
	}
}

type fakeProfileStore struct {
	unknown []UnknownDeviceRef
	updates []string // "userID/deviceID/platform"
	fail    bool
}

func (f *fakeProfileStore) UnknownPlatformDevices(context.Context) ([]UnknownDeviceRef, error) {
	if f.fail {
		return nil, errors.New("store down")
	}
	return f.unknown, nil
}

func (f *fakeProfileStore) UpdateDevicePlatform(_ context.Context, userID, deviceID, platform string) error {
	f.updates = append(f.updates, userID+"/"+deviceID+"/"+platform)
	return nil
}

func TestProfilerPassInfersPlatformBySourceIP(t *testing.T) {
	store := &fakeProfileStore{unknown: []UnknownDeviceRef{
		{UserID: "u1", DeviceID: "d1", SourceIP: "203.0.113.9"},
		{UserID: "u1", DeviceID: "d2", SourceIP: "198.51.100.7"}, // 无匹配连接，不推断
	}}
	profiler := &DeviceProfiler{
		Snapshot: func(context.Context) ([]ConnectionSample, error) {
			return []ConnectionSample{
				{SourceIP: "203.0.113.9", Host: "dns.msftncsi.com"},
				{SourceIP: "203.0.113.9", Host: "www.msftconnecttest.com"},
				{SourceIP: "203.0.113.9", Host: "www.google.com"},
				{SourceIP: "198.51.100.7", Host: "swscan.apple.com"}, // 单域名不足阈值
			}, nil
		},
		Store: store,
		Log:   func(slog.Level, string) {},
	}
	profiler.pass(t.Context())
	if len(store.updates) != 1 || store.updates[0] != "u1/d1/windows" {
		t.Fatalf("updates=%v, want exactly u1/d1/windows", store.updates)
	}
	// 推断成功后累积被清空：再次 pass 不得重复更新。
	store.unknown = nil
	profiler.pass(t.Context())
	if len(store.updates) != 1 {
		t.Fatalf("second pass must not re-update, updates=%v", store.updates)
	}
}

func TestProfilerPassToleratesSnapshotFailure(t *testing.T) {
	store := &fakeProfileStore{unknown: []UnknownDeviceRef{{UserID: "u1", DeviceID: "d1", SourceIP: "203.0.113.9"}}}
	profiler := &DeviceProfiler{
		Snapshot: func(context.Context) ([]ConnectionSample, error) { return nil, errors.New("clash api down") },
		Store:    store,
		Log:      func(slog.Level, string) {},
	}
	profiler.pass(t.Context())
	if len(store.updates) != 0 {
		t.Fatalf("snapshot failure must not update, updates=%v", store.updates)
	}
}

func TestNetworkKeyFoldsPrefix(t *testing.T) {
	cases := []struct{ ip, want string }{
		{"203.0.113.9", "203.0.113.0/24"},
		{"203.0.113.117", "203.0.113.0/24"},       // 同 /24 不同址 → 同 key
		{"198.51.100.7", "198.51.100.0/24"},       // 跨 /24 → 不同 key
		{"2001:db8:a:b::1", "20010db8000a::/48"}, //
		{"2001:db8:a:f::2", "20010db8000a::/48"}, // 同 /48 不同址 → 同 key
		{"2001:db8:ff::1", "20010db8000ff::/48"},  // 跨 /48 → 不同 key
		{"not-an-ip", "not-an-ip"},                // 解析失败原样返回
	}
	for _, tc := range cases {
		if got := networkKey(tc.ip); got != tc.want {
			t.Errorf("networkKey(%q)=%q, want %q", tc.ip, got, tc.want)
		}
	}
}

// TestProfilerPassAttributesByPrefix 固定前缀归因：家宽动态出口下，设备
// 记录的 source_ip 与实时连接源地址同 /24（IPv4）或同 /48（IPv6）即可
// 归因；跨前缀不得串画像。
func TestProfilerPassAttributesByPrefix(t *testing.T) {
	store := &fakeProfileStore{unknown: []UnknownDeviceRef{
		{UserID: "u1", DeviceID: "d-v4", SourceIP: "203.0.113.99"},   // 记录的是旧出口
		{UserID: "u1", DeviceID: "d-v6", SourceIP: "2001:db8:a::1"},  // 同上，IPv6
		{UserID: "u1", DeviceID: "d-far", SourceIP: "198.51.100.112"}, // 跨 /24，不得串
	}}
	profiler := &DeviceProfiler{
		Snapshot: func(context.Context) ([]ConnectionSample, error) {
			return []ConnectionSample{
				// 当前连接源地址已漂移到同 /24 的另一地址。
				{SourceIP: "203.0.113.117", Host: "dns.msftncsi.com"},
				{SourceIP: "203.0.113.117", Host: "www.msftconnecttest.com"},
				// IPv6 同 /48 漂移。
				{SourceIP: "2001:db8:a:ffff::9", Host: "swscan.apple.com"},
				{SourceIP: "2001:db8:a:ffff::9", Host: "swcdn.apple.com"},
				// 跨 /24 的信号不得归给 d-far。
				{SourceIP: "198.51.100.240", Host: "mtalk.google.com"},
			}, nil
		},
		Store: store,
		Log:   func(slog.Level, string) {},
	}
	profiler.pass(t.Context())
	want := map[string]bool{"u1/d-v4/windows": true, "u1/d-v6/macos": true}
	if len(store.updates) != len(want) {
		t.Fatalf("updates=%v, want %v", store.updates, want)
	}
	for _, update := range store.updates {
		if !want[update] {
			t.Fatalf("unexpected update %q, want only %v", update, want)
		}
	}
}
