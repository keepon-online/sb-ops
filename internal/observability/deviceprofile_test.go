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
