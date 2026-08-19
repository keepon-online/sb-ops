package traffic

import (
	"context"
	"testing"

	"github.com/google/uuid"
	v2rayapi "github.com/sagernet/sing-box/experimental/v2rayapi"
)

func TestParseV2RayDeviceStat(t *testing.T) {
	deviceID := uuid.NewString()
	cases := []struct {
		name, device, dir string
		ok                bool
	}{
		{"user>>>" + deviceID + ">>>traffic>>>uplink", deviceID, "uplink", true},
		{"user>>>" + deviceID + ">>>traffic>>>downlink", deviceID, "downlink", true},
		{"user>>>not-a-uuid>>>traffic>>>uplink", "", "", false},
		{"system>>>" + deviceID + ">>>traffic>>>uplink", "", "", false},
		{"user>>>" + deviceID + ">>>traffic>>>other", "", "", false},
	}
	for _, tc := range cases {
		device, dir, ok := parseV2RayDeviceStat(tc.name)
		if device != tc.device || dir != tc.dir || ok != tc.ok {
			t.Fatalf("parse(%q)=%q,%q,%v want %q,%q,%v", tc.name, device, dir, ok, tc.device, tc.dir, tc.ok)
		}
	}
}

func TestV2rayCollectorAttributesPerDevice(t *testing.T) {
	userA, userB := uuid.NewString(), uuid.NewString()
	deviceA, deviceB := uuid.NewString(), uuid.NewString()
	collector := newV2rayCollector(func(context.Context) (string, bool) {
		return "127.0.0.1:10000", true
	}, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{deviceA: userA, deviceB: userB}, nil
	})
	collector.query = func(_ context.Context, _ *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
		return &v2rayapi.QueryStatsResponse{Stat: []*v2rayapi.Stat{
			{Name: "user>>>" + deviceA + ">>>traffic>>>uplink", Value: 100},
			{Name: "user>>>" + deviceB + ">>>traffic>>>downlink", Value: 200},
			// 已删除设备的残留计数器应被丢弃。
			{Name: "user>>>" + uuid.NewString() + ">>>traffic>>>uplink", Value: 999},
		}}, nil
	}

	samples, _, err := collector.collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("samples=%+v", samples)
	}
	byDevice := map[string]Sample{}
	for _, sample := range samples {
		byDevice[sample.DeviceID] = sample
	}
	if byDevice[deviceA].UserID != userA || byDevice[deviceA].UpBytes != 100 {
		t.Fatalf("deviceA sample=%+v", byDevice[deviceA])
	}
	if byDevice[deviceB].UserID != userB || byDevice[deviceB].DownBytes != 200 {
		t.Fatalf("deviceB sample=%+v", byDevice[deviceB])
	}
}

func TestV2rayCollectorAttributesPerProtocol(t *testing.T) {
	collector := newV2rayCollector(func(context.Context) (string, bool) {
		return "127.0.0.1:10000", true
	}, func(_ context.Context, tag string) (string, bool) {
		if tag == "vless-main" {
			return "proto-1", true
		}
		return "", false
	}, nil)
	collector.query = func(_ context.Context, _ *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
		return &v2rayapi.QueryStatsResponse{Stat: []*v2rayapi.Stat{
			{Name: "inbound>>>vless-main>>>traffic>>>uplink", Value: 150},
			{Name: "inbound>>>unknown-tag>>>traffic>>>downlink", Value: 999},
		}}, nil
	}
	samples, _, err := collector.collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("samples=%+v", samples)
	}
	if samples[0].ProtocolID != "proto-1" || samples[0].UserID != "" || samples[0].DeviceID != "" || samples[0].UpBytes != 150 {
		t.Fatalf("sample=%+v", samples[0])
	}
}

func TestParseV2RayInboundStat(t *testing.T) {
	for _, tc := range []struct {
		name, tag, dir string
		ok             bool
	}{
		{"inbound>>>vless-main>>>traffic>>>uplink", "vless-main", "uplink", true},
		{"inbound>>>vless-main>>>traffic>>>downlink", "vless-main", "downlink", true},
		{"user>>>x>>>traffic>>>uplink", "", "", false},
		{"inbound>>>vless-main>>>traffic>>>other", "", "", false},
	} {
		tag, dir, ok := parseV2RayInboundStat(tc.name)
		if tag != tc.tag || dir != tc.dir || ok != tc.ok {
			t.Fatalf("parse(%q)=%q,%q,%v want %q,%q,%v", tc.name, tag, dir, ok, tc.tag, tc.dir, tc.ok)
		}
	}
}

func TestV2rayCollectorReportsNotConfigured(t *testing.T) {
	collector := newV2rayCollector(func(context.Context) (string, bool) {
		return "", false
	}, nil, nil)
	if _, _, err := collector.collect(context.Background()); err == nil {
		t.Fatal("expected error when the resolver reports the API is not configured")
	}
}

func TestV2rayCollectorPrunesVanishedDimensions(t *testing.T) {
	deviceA := uuid.NewString()
	withDevice := []*v2rayapi.Stat{{Name: "user>>>" + deviceA + ">>>traffic>>>uplink", Value: 100}}
	var stats []*v2rayapi.Stat
	collector := newV2rayCollector(func(context.Context) (string, bool) {
		return "127.0.0.1:10000", true
	}, nil, func(context.Context) (map[string]string, error) {
		return map[string]string{deviceA: uuid.NewString()}, nil
	})
	collector.query = func(_ context.Context, _ *v2rayapi.QueryStatsRequest) (*v2rayapi.QueryStatsResponse, error) {
		return &v2rayapi.QueryStatsResponse{Stat: stats}, nil
	}

	stats = withDevice
	if samples, pruned, err := collector.collect(context.Background()); err != nil || len(samples) != 1 || len(pruned.DeviceIDs) != 0 {
		t.Fatalf("collect: %v samples=%+v pruned=%+v", err, samples, pruned)
	}

	// Two consecutive absences keep the dimension (transient gap); a
	// reappearance in between resets the absence counter.
	for i := 0; i < 2; i++ {
		stats = nil
		if samples, pruned, err := collector.collect(context.Background()); err != nil || len(samples) != 1 || len(pruned.DeviceIDs) != 0 {
			t.Fatalf("absence %d: %v samples=%+v pruned=%+v", i, err, samples, pruned)
		}
	}
	stats = withDevice
	if _, _, err := collector.collect(context.Background()); err != nil {
		t.Fatalf("collect: %v", err)
	}

	// Three consecutive absences prune the dimension and report it so the
	// cursors can be dropped.
	stats = nil
	for i := 0; i < pruneAbsenceThreshold-1; i++ {
		if _, pruned, err := collector.collect(context.Background()); err != nil || len(pruned.DeviceIDs) != 0 {
			t.Fatalf("absence %d: %v pruned=%+v", i, err, pruned)
		}
	}
	samples, pruned, err := collector.collect(context.Background())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(samples) != 0 || len(pruned.DeviceIDs) != 1 || pruned.DeviceIDs[0] != deviceA {
		t.Fatalf("samples=%+v pruned=%+v", samples, pruned)
	}
	if len(collector.deviceTotals) != 0 || len(collector.deviceAbsent) != 0 {
		t.Fatalf("collector state deviceTotals=%v deviceAbsent=%v", collector.deviceTotals, collector.deviceAbsent)
	}
}
