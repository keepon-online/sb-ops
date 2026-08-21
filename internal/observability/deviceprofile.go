package observability

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/gookit/slog"
)

// DeviceProfiler infers the platform of devices the subscription User-Agent
// could not identify (e.g. "clash-verge/2.5.2" carries no OS hint) from the
// domains they actually connect to: a Windows box keeps hitting Microsoft
// update/NCSI probes, macOS pulls Apple software updates, Android maintains
// its FCM push connection. Connections are attributed to devices by matching
// the connection source address against the device's subscription source
// address (devices.source_ip). Devices sharing one egress address share the
// portrait — acceptable for platform inference and strictly better than a
// permanent "unknown".
type DeviceProfiler struct {
	// Snapshot fetches the current Clash connection list. Only the source
	// address and target host of each connection are consumed.
	Snapshot func(ctx context.Context) ([]ConnectionSample, error)
	// Store lists unknown-platform devices and persists inferred platforms.
	Store DeviceProfileStore
	// Interval between portrait passes; defaults to profileInterval.
	Interval time.Duration
	// Log receives lifecycle messages; defaults to the gookit global logger.
	Log func(level slog.Level, message string)

	mu        sync.Mutex
	portraits map[deviceKey]map[string]map[string]bool
}

// ConnectionSample is the projection of one live connection the profiler needs.
type ConnectionSample struct {
	SourceIP string
	Host     string
}

// DeviceProfileStore is the persistence side of inference, satisfied by the
// user service.
type DeviceProfileStore interface {
	UnknownPlatformDevices(ctx context.Context) ([]UnknownDeviceRef, error)
	UpdateDevicePlatform(ctx context.Context, userID, deviceID, platform string) error
}

// UnknownDeviceRef is the store-side mirror of the unknown-device reference;
// kept as a local type so observability stays free of service imports
// (internal/database imports this package).
type UnknownDeviceRef struct {
	UserID   string
	DeviceID string
	SourceIP string
}

type deviceKey struct{ userID, deviceID string }

const (
	profileInterval  = 2 * time.Minute
	profileThreshold = 2  // distinct signal domains required before inferring
	profileDomainCap = 32 // per device+platform domain memory cap
)

// platformDomains maps a platform to high-confidence domain suffixes. Only
// suffixes that essentially never appear on other platforms are listed;
// anything ambiguous (e.g. push.apple.com serves iOS and macOS alike) is left
// out so inference errs on the side of staying unknown.
var platformDomains = map[string][]string{
	"windows": {
		"msftconnecttest.com", // Windows NCSI connectivity probe
		"msftncsi.com",
		"windowsupdate.com",
		"update.microsoft.com",
		"delivery.mp.microsoft.com",
	},
	"macos": {
		"swscan.apple.com", // macOS software update catalog
		"swcdn.apple.com",
		"swdownload.apple.com",
	},
	"android": {
		"mtalk.google.com", // FCM push long connection
		"play.googleapis.com",
		"gvt1.com",
	},
}

// domainPlatform resolves one host to a platform signal, matching exact or
// dot-boundary suffixes ("a.mtalk.google.com" hits, "notmtalk.google.com"
// does not).
func domainPlatform(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	if host == "" {
		return "", false
	}
	for platform, suffixes := range platformDomains {
		for _, suffix := range suffixes {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return platform, true
			}
		}
	}
	return "", false
}

func (p *DeviceProfiler) interval() time.Duration {
	if p.Interval > 0 {
		return p.Interval
	}
	return profileInterval
}

func (p *DeviceProfiler) log(level slog.Level, message string) {
	if p.Log != nil {
		p.Log(level, message)
		return
	}
	slog.Log(level, message)
}

// Run loops until ctx is done, refreshing portraits every Interval.
func (p *DeviceProfiler) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.pass(ctx)
		}
	}
}

// pass runs one snapshot: attribute live connection hosts to unknown devices
// by source address, then persist any platform that reached the threshold.
func (p *DeviceProfiler) pass(ctx context.Context) {
	if p.Snapshot == nil || p.Store == nil {
		return
	}
	devices, err := p.Store.UnknownPlatformDevices(ctx)
	if err != nil {
		p.log(slog.WarnLevel, "读取待识别设备失败: "+err.Error())
		return
	}
	if len(devices) == 0 {
		p.mu.Lock()
		p.portraits = nil
		p.mu.Unlock()
		return
	}
	samples, err := p.Snapshot(ctx)
	if err != nil {
		p.log(slog.WarnLevel, "读取实时连接失败（平台推断跳过本轮）: "+err.Error())
		return
	}
	hostsByIP := make(map[string]map[string]bool, len(devices))
	for _, sample := range samples {
		if sample.SourceIP == "" || sample.Host == "" {
			continue
		}
		hosts, ok := hostsByIP[sample.SourceIP]
		if !ok {
			hosts = map[string]bool{}
			hostsByIP[sample.SourceIP] = hosts
		}
		hosts[strings.ToLower(sample.Host)] = true
	}
	if p.portraits == nil {
		p.portraits = map[deviceKey]map[string]map[string]bool{}
	}
	live := make(map[deviceKey]bool, len(devices))
	for _, device := range devices {
		key := deviceKey{userID: device.UserID, deviceID: device.DeviceID}
		live[key] = true
		for host := range hostsByIP[device.SourceIP] {
			platform, ok := domainPlatform(host)
			if !ok {
				continue
			}
			p.observe(key, platform, host)
		}
	}
	p.mu.Lock()
	for key := range p.portraits {
		if !live[key] {
			delete(p.portraits, key)
		}
	}
	portraits := p.portraits
	p.mu.Unlock()
	for key, platforms := range portraits {
		platform, ok := inferPlatform(platforms)
		if !ok {
			continue
		}
		if err := p.Store.UpdateDevicePlatform(ctx, key.userID, key.deviceID, platform); err != nil {
			p.log(slog.WarnLevel, "写入设备平台推断失败: "+err.Error())
			continue
		}
		p.mu.Lock()
		delete(p.portraits, key)
		p.mu.Unlock()
		p.log(slog.InfoLevel, "设备平台已自动识别: device="+key.deviceID+" platform="+platform)
	}
}

func (p *DeviceProfiler) observe(key deviceKey, platform, host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	platforms, ok := p.portraits[key]
	if !ok {
		platforms = map[string]map[string]bool{}
		p.portraits[key] = platforms
	}
	domains, ok := platforms[platform]
	if !ok {
		domains = map[string]bool{}
		platforms[platform] = domains
	}
	if len(domains) >= profileDomainCap {
		return
	}
	domains[host] = true
}

// inferPlatform returns the platform once its distinct signal-domain count
// reaches the threshold; ties resolve to the lexicographically first platform
// deterministically (extremely unlikely — the domains are disjoint).
func inferPlatform(platforms map[string]map[string]bool) (string, bool) {
	best := ""
	for platform, domains := range platforms {
		if len(domains) < profileThreshold {
			continue
		}
		if best == "" || platform < best {
			best = platform
		}
	}
	return best, best != ""
}
