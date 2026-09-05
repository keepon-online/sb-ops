package report

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildDailyHappyPath(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.Local)
	sources := Sources{
		YesterdayTraffic:    func(context.Context) (int64, int64, error) { return 1288490188, 8912896000, nil },
		ActiveUsers:         func(context.Context) (int64, error) { return 4, nil },
		AlertStats:          func(context.Context) (int, int, int, error) { return 1, 2, 3, nil },
		LatestBackup:        func() (int64, int, error) { return 1757131200, 12, nil },
		BackupScheduleError: func() string { return "" },
		Bandwidth: func() (int64, int64, float64, bool) {
			return 335 * 1024 * 1024 * 1024, 1024 * 1024 * 1024 * 1024, 32.7, true
		},
	}

	report := BuildDaily(context.Background(), now, sources)

	if report.EventID != "report-daily-20260905" {
		t.Fatalf("EventID=%s, want report-daily-20260905", report.EventID)
	}
	if !strings.Contains(report.Title, "2026-09-05") {
		t.Fatalf("Title should cover yesterday: %s", report.Title)
	}
	for _, keyword := range []string{
		"上行 1.2 GB", "下行 8.3 GB", "合计 9.5 GB",
		"活跃用户：4",
		"当前开放 1 · 昨日新增 2 / 恢复 3",
		"共 12 份", "定时备份正常",
		"VPS 月流量：335.0 GB / 1.0 TB（32.7%）",
	} {
		if !strings.Contains(report.Message, keyword) {
			t.Errorf("message missing %q:\n%s", keyword, report.Message)
		}
	}
}

func TestBuildDailyMarksItemFailures(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.Local)
	sources := Sources{
		YesterdayTraffic:    func(context.Context) (int64, int64, error) { return 0, 0, errors.New("db down") },
		ActiveUsers:         func(context.Context) (int64, error) { return 0, errors.New("db down") },
		AlertStats:          func(context.Context) (int, int, int, error) { return 0, 0, 0, nil },
		LatestBackup:        func() (int64, int, error) { return 0, 0, errors.New("io") },
		BackupScheduleError: func() string { return "VACUUM INTO 失败" },
		Bandwidth:           func() (int64, int64, float64, bool) { return 0, 0, 0, false },
	}

	report := BuildDaily(context.Background(), now, sources)

	for _, keyword := range []string{"昨日流量：获取失败", "活跃用户：获取失败", "备份：获取失败"} {
		if !strings.Contains(report.Message, keyword) {
			t.Errorf("message missing %q:\n%s", keyword, report.Message)
		}
	}
	// 备份行拉取失败时不再拼定时备份状态；带宽无采样降级提示。
	if strings.Contains(report.Message, "定时备份") && strings.Contains(report.Message, "备份：获取失败") {
		t.Fatalf("backup failure line should not mix schedule state:\n%s", report.Message)
	}
	if !strings.Contains(report.Message, "VPS 月流量：暂无采样") {
		t.Fatalf("bandwidth should show no-sample hint:\n%s", report.Message)
	}
}

func TestBuildDailyShowsScheduleError(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.Local)
	sources := Sources{
		YesterdayTraffic:    func(context.Context) (int64, int64, error) { return 1, 2, nil },
		ActiveUsers:         func(context.Context) (int64, error) { return 1, nil },
		AlertStats:          func(context.Context) (int, int, int, error) { return 0, 0, 0, nil },
		LatestBackup:        func() (int64, int, error) { return 1757131200, 3, nil },
		BackupScheduleError: func() string { return "磁盘已满" },
		Bandwidth:           func() (int64, int64, float64, bool) { return 0, 0, 0, false },
	}
	report := BuildDaily(context.Background(), now, sources)
	if !strings.Contains(report.Message, "定时备份最近失败：磁盘已满") {
		t.Fatalf("schedule error should surface:\n%s", report.Message)
	}
}

func TestBuildDailyWithoutBackups(t *testing.T) {
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.Local)
	sources := Sources{
		YesterdayTraffic:    func(context.Context) (int64, int64, error) { return 1, 2, nil },
		ActiveUsers:         func(context.Context) (int64, error) { return 0, nil },
		AlertStats:          func(context.Context) (int, int, int, error) { return 0, 0, 0, nil },
		LatestBackup:        func() (int64, int, error) { return 0, 0, nil },
		BackupScheduleError: func() string { return "" },
		Bandwidth:           func() (int64, int64, float64, bool) { return 0, 0, 0, false },
	}
	report := BuildDaily(context.Background(), now, sources)
	if !strings.Contains(report.Message, "备份：暂无快照") {
		t.Fatalf("zero backups should show hint:\n%s", report.Message)
	}
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("CST", 8*60*60)
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before", time.Date(2026, 9, 6, 7, 59, 0, 0, loc), time.Date(2026, 9, 6, 8, 0, 0, 0, loc)},
		{"at", time.Date(2026, 9, 6, 8, 0, 0, 0, loc), time.Date(2026, 9, 7, 8, 0, 0, 0, loc)},
		{"after", time.Date(2026, 9, 6, 20, 0, 0, 0, loc), time.Date(2026, 9, 7, 8, 0, 0, 0, loc)},
		{"midnight hour", time.Date(2026, 9, 6, 0, 30, 0, 0, loc), time.Date(2026, 9, 7, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := NextRun(c.now, c.want.Hour()); !got.Equal(c.want) {
			t.Errorf("%s: NextRun(%v)=%v, want %v", c.name, c.now, got, c.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		value int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1288490188, "1.2 GB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.value); got != c.want {
			t.Errorf("FormatBytes(%d)=%s, want %s", c.value, got, c.want)
		}
	}
}
