package share

import (
	"regexp"
	"sort"
	"strings"
)

// normalizeClientToken 把 UA 应用段压成纯小写字母：去空格、连字符、点、
// 下划线，再按斜杠切出应用名（"clash-verge" 与 "Clash Verge"、
// "clash.meta" 等形态统一为同一键）。
func normalizeClientToken(userAgent string) string {
	compact := strings.NewReplacer(" ", "", "_", "", "-", "", ".", "").Replace(strings.ToLower(userAgent))
	return strings.SplitN(compact, "/", 2)[0]
}

// UAInfo 是订阅请求中可安全持久化的客户端观测结果。Key 只由客户端
// 应用名决定，版本变化不会创建新设备；Version 仅用于展示最近一次观测值。
// AutoCreate 为 false 时（浏览器样式的 UA）Key 仅用于匹配既有设备——
// 客户端可能被手动改成浏览器 UA 长期使用，不能丢路由；但浏览器直接打开
// 订阅链接不应触发自动建设备。
type UAInfo struct {
	Key        string
	Label      string
	Client     string
	Platform   string
	Version    string
	AutoCreate bool
}

// ParseUserAgent 识别订阅下载请求的客户端与平台。平台无法从 UA 可靠判断时
// 返回 unknown，不根据客户端名称猜测跨平台应用的操作系统。
func ParseUserAgent(userAgent string) UAInfo {
	trimmed := strings.TrimSpace(userAgent)
	if trimmed == "" {
		return UAInfo{Platform: "unknown"}
	}
	fields := strings.Fields(trimmed)
	token := normalizeClientToken(trimmed)
	for _, client := range uaClients {
		if strings.HasPrefix(token, client.prefix) {
			return UAInfo{
				Key: client.prefix, Label: client.label, Client: client.label,
				Platform: detectPlatform(trimmed), Version: extractVersion(trimmed, client.prefix),
				AutoCreate: true,
			}
		}
	}
	if isBrowserUA(trimmed) {
		// 浏览器样式的 UA：保留稳定 key 以匹配既有设备（手动改 UA 的客户端
		// 不受影响），但不自动新建，浏览器预览订阅链接不再消耗设备配额。
		return UAInfo{
			Key: "ua-" + token, Label: truncateUA(fields[0]), Client: truncateUA(fields[0]),
			Platform: detectPlatform(trimmed), Version: extractVersion(trimmed, token),
		}
	}
	// 未匹配到受支持客户端，仍保留稳定 key，兼容当前自动发现行为；
	// 展示名截断到安全长度。
	return UAInfo{
		Key: "ua-" + token, Label: truncateUA(fields[0]), Client: truncateUA(fields[0]),
		Platform: detectPlatform(trimmed), Version: extractVersion(trimmed, token),
		AutoCreate: true,
	}
}

// isBrowserUA 判断 UA 是否来自浏览器。浏览器 UA 以 "Mozilla/" 开头且通常
// 携带渲染引擎/浏览器标识；Clash 系客户端 UA（如 "clash-verge/v1.7.7"）不含
// 这些标识，因此不受影响。
func isBrowserUA(userAgent string) bool {
	lower := strings.ToLower(userAgent)
	if !strings.HasPrefix(lower, "mozilla/") {
		return false
	}
	browserTokens := []string{"chrome", "safari", "firefox", "edg/", "trident", "opera", "opr/"}
	for _, token := range browserTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

var versionPattern = regexp.MustCompile(`(?i)(?:^|[/ _-])v?([0-9]+(?:\.[0-9A-Za-z-]+)+)`)

func extractVersion(userAgent, token string) string {
	parts := strings.SplitN(strings.TrimSpace(userAgent), "/", 2)
	if len(parts) == 2 && strings.HasPrefix(normalizeClientToken(parts[0]), token) {
		version := strings.Fields(parts[1])
		if len(version) > 0 {
			return truncateUA(strings.Trim(version[0], "()"))
		}
	}
	if match := versionPattern.FindStringSubmatch(userAgent); len(match) == 2 {
		return truncateUA(match[1])
	}
	return ""
}

func detectPlatform(userAgent string) string {
	value := strings.ToLower(userAgent)
	switch {
	case strings.Contains(value, "android"):
		return "android"
	case strings.Contains(value, "iphone"), strings.Contains(value, "ipad"), strings.Contains(value, "ios"):
		return "ios"
	case strings.Contains(value, "windows"):
		return "windows"
	case strings.Contains(value, "mac os"), strings.Contains(value, "macos"):
		return "macos"
	case strings.Contains(value, "linux"):
		return "linux"
	default:
		return "unknown"
	}
}

func truncateUA(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		return value[:128]
	}
	return value
}

type uaClient struct{ prefix, label string }

// uaClients 覆盖常见 Clash 系客户端，前缀为 normalizeClientToken 形态
// （纯小写字母），按长度降序排列避免 "clash" 截胡 "clashverge"。
var uaClients = func() []uaClient {
	clients := []uaClient{
		{prefix: "clashverge", label: "Clash Verge"},
		{prefix: "clashforwindows", label: "Clash for Windows"},
		{prefix: "clashmetaforandroid", label: "Clash Meta Android"},
		{prefix: "clashmeta", label: "Clash Meta"},
		{prefix: "mihomo", label: "mihomo"},
		{prefix: "clash", label: "Clash"},
		{prefix: "stash", label: "Stash"},
		{prefix: "shadowrocket", label: "Shadowrocket"},
		{prefix: "singbox", label: "sing-box"},
		{prefix: "sfm", label: "sing-box (SFM)"},
		{prefix: "v2rayn", label: "v2rayN"},
		{prefix: "nekobox", label: "NekoBox"},
		{prefix: "hiddify", label: "Hiddify"},
		{prefix: "loon", label: "Loon"},
		{prefix: "surge", label: "Surge"},
		{prefix: "quantumult", label: "Quantumult"},
	}
	sort.Slice(clients, func(i, j int) bool { return len(clients[i].prefix) > len(clients[j].prefix) })
	return clients
}()
