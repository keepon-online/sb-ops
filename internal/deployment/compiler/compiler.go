package compiler

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

func Compile(input Input) (Result, error) {
	if err := input.Validate(); err != nil {
		return Result{}, err
	}
	protocols := append([]serviceprotocol.Protocol(nil), input.Protocols...)
	sort.Slice(protocols, func(i, j int) bool { return protocols[i].Name < protocols[j].Name })
	users := append([]serviceuser.ArtifactUser(nil), input.Users...)
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	devices := append([]serviceuser.ArtifactDevice(nil), input.Devices...)
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].UserID != devices[j].UserID {
			return devices[i].UserID < devices[j].UserID
		}
		return devices[i].DeviceID < devices[j].DeviceID
	})
	now := inputNow()
	userByID := make(map[string]serviceuser.ArtifactUser, len(users))
	available := func(userID string) bool {
		user, ok := userByID[userID]
		return ok && user.AvailableAt(now)
	}
	for _, user := range users {
		userByID[user.ID] = user
	}
	// V2Ray stats 白名单按设备展开：入站用户 name 是设备 id，采集器据此把
	// 流量归因到设备（再经映射折算到用户）。
	trafficUsers := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.Enabled && available(device.UserID) {
			trafficUsers = append(trafficUsers, device.DeviceID)
		}
	}

	inbounds := make([]any, 0, len(protocols))
	inboundTags := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		if !protocol.Enabled {
			continue
		}
		inboundTags = append(inboundTags, protocol.InboundTag())
		bound := boundDevices(devices, available, input.UserProtocols, protocol.ID)
		switch protocol.Type {
		case serviceprotocol.TypeVLESS:
			inbounds = append(inbounds, vlessInbound(protocol, bound, input.tlsPaths(protocol)))
		case serviceprotocol.TypeVMess:
			inbounds = append(inbounds, vmessInbound(protocol, bound, input.tlsPaths(protocol)))
		case serviceprotocol.TypeHysteria2:
			paths := input.tlsPaths(protocol)
			inbounds = append(inbounds, hysteriaInbound(protocol, bound, paths.CertificatePath, paths.KeyPath))
		case serviceprotocol.TypeTUIC:
			paths := input.tlsPaths(protocol)
			inbounds = append(inbounds, tuicInbound(protocol, bound, paths.CertificatePath, paths.KeyPath))
		case serviceprotocol.TypeAnyTLS:
			paths := input.tlsPaths(protocol)
			inbounds = append(inbounds, anyTLSInbound(protocol, bound, paths.CertificatePath, paths.KeyPath))
		case serviceprotocol.TypeShadowsocks:
			inbounds = append(inbounds, shadowsocksInbound(protocol, bound))
		case serviceprotocol.TypeTrojan:
			inbounds = append(inbounds, trojanInbound(protocol, bound, input.tlsPaths(protocol)))
		}
	}
	outbounds, endpoints, err := singBoxOutbounds(input.Outbounds, input.OutboundPasswords)
	if err != nil {
		return Result{}, err
	}
	server := map[string]any{
		"log":       singBoxLog(input.SingBoxConfig),
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     singBoxRoute(input.SingBoxConfig, input.RouteRules, input.RuleSets),
	}
	if len(endpoints) > 0 {
		server["endpoints"] = endpoints
	}
	if experimental := singBoxExperimental(input.SingBoxConfig, input.ClashAPISecret, trafficUsers, inboundTags, input.CacheFilePath); experimental != nil {
		server["experimental"] = experimental
	}
	if dns := singBoxDNS(input.SingBoxConfig); dns != nil {
		server["dns"] = dns
	}
	raw, err := json.MarshalIndent(server, "", "  ")
	if err != nil {
		return Result{}, err
	}
	result := Result{ServerConfig: append(raw, '\n'), Subscriptions: []Subscription{}}
	for _, user := range users {
		if !user.AvailableAt(now) {
			continue
		}
		for _, device := range devices {
			if device.UserID != user.ID || !device.Enabled {
				continue
			}
			content, err := clashSubscription(input, protocols, user, device)
			if err != nil {
				return Result{}, err
			}
			result.Subscriptions = append(result.Subscriptions, Subscription{UserID: user.ID, DeviceID: device.DeviceID, Content: content})
		}
	}
	return result, nil
}

func singBoxLog(config servicesingbox.Config) map[string]any {
	level := config.LogLevel
	if level == "" {
		level = "info"
	}
	return map[string]any{"level": level, "timestamp": config.LogTimestamp || config.LogLevel == ""}
}

// singBoxOutbounds splits managed outbounds into the sing-box outbounds array
// (SOCKS) and the endpoints array (WARP wireguard). Endpoints are dialable by
// tag from route rules and DNS detours, same as outbounds.
func singBoxOutbounds(outbounds []servicesingbox.Outbound, passwords map[string]string) ([]any, []any, error) {
	values := []any{map[string]any{"type": "direct", "tag": "direct"}, map[string]any{"type": "block", "tag": "block"}}
	endpoints := []any{}
	for _, outbound := range outbounds {
		switch outbound.Type {
		case servicesingbox.OutboundTypeSOCKS:
			value := map[string]any{"type": "socks", "tag": outbound.Tag, "server": outbound.Server, "server_port": outbound.ServerPort}
			if outbound.Username != "" {
				value["username"] = outbound.Username
			}
			if password := passwords[outbound.Tag]; password != "" {
				value["password"] = password
			}
			values = append(values, value)
		case servicesingbox.OutboundTypeWarp:
			if outbound.Warp == nil {
				continue
			}
			endpoint, err := warpEndpoint(outbound.Tag, outbound.Warp)
			if err != nil {
				return nil, nil, err
			}
			endpoints = append(endpoints, endpoint)
		}
	}
	return values, endpoints, nil
}

func warpEndpoint(tag string, settings *servicesingbox.WarpSettings) (map[string]any, error) {
	host, portText, err := net.SplitHostPort(settings.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("出站 %q 的 WARP endpoint 不是 host:port: %w", tag, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("出站 %q 的 WARP endpoint 端口无效", tag)
	}
	address := []string{settings.IPv4}
	if settings.IPv6 != "" {
		address = append(address, settings.IPv6)
	}
	peer := map[string]any{
		"address":     host,
		"port":        port,
		"public_key":  settings.PublicKey,
		"allowed_ips": []string{"0.0.0.0/0", "::/0"},
	}
	if len(settings.Reserved) == 3 {
		peer["reserved"] = settings.Reserved
	}
	endpoint := map[string]any{"type": "wireguard", "tag": tag, "address": address, "private_key": settings.PrivateKey, "peers": []any{peer}}
	if settings.MTU > 0 {
		endpoint["mtu"] = settings.MTU
	}
	return endpoint, nil
}

func singBoxRoute(config servicesingbox.Config, rules []servicesingbox.RouteRule, ruleSets []servicesingbox.RuleSet) map[string]any {
	final := config.DefaultOutboundTag
	if final == "" {
		final = "direct"
	}
	values := make([]any, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		value := map[string]any{"outbound": rule.OutboundTag}
		if len(rule.Conditions.Domains) > 0 {
			value["domain"] = rule.Conditions.Domains
		}
		if len(rule.Conditions.DomainSuffixes) > 0 {
			value["domain_suffix"] = rule.Conditions.DomainSuffixes
		}
		if len(rule.Conditions.IPCIDRs) > 0 {
			value["ip_cidr"] = rule.Conditions.IPCIDRs
		}
		if len(rule.Conditions.Ports) > 0 {
			value["port"] = rule.Conditions.Ports
		}
		if len(rule.Conditions.Networks) > 0 {
			value["network"] = rule.Conditions.Networks
		}
		if len(rule.Conditions.InboundTags) > 0 {
			value["inbound"] = rule.Conditions.InboundTags
		}
		if len(rule.Conditions.GeoIP) > 0 {
			value["geoip"] = rule.Conditions.GeoIP
		}
		if len(rule.Conditions.GeoSite) > 0 {
			value["geosite"] = rule.Conditions.GeoSite
		}
		if len(rule.Conditions.RuleSets) > 0 {
			value["rule_set"] = rule.Conditions.RuleSets
		}
		values = append(values, value)
	}
	result := map[string]any{"rules": values, "final": final}
	if definitions := singBoxRuleSets(ruleSets); len(definitions) > 0 {
		result["rule_set"] = definitions
	}
	return result
}

func singBoxRuleSets(ruleSets []servicesingbox.RuleSet) []any {
	values := make([]any, 0, len(ruleSets))
	for _, ruleSet := range ruleSets {
		value := map[string]any{"type": "remote", "tag": ruleSet.Tag, "format": ruleSet.Format, "url": ruleSet.URL}
		if ruleSet.DownloadDetour != "" {
			value["download_detour"] = ruleSet.DownloadDetour
		}
		values = append(values, value)
	}
	return values
}

func singBoxDNS(config servicesingbox.Config) map[string]any {
	if len(config.DNSServers) == 0 {
		return nil
	}
	servers := make([]any, 0, len(config.DNSServers))
	for _, server := range config.DNSServers {
		value := map[string]any{"tag": server.Tag, "address": server.Address}
		if server.Detour != "" {
			value["detour"] = server.Detour
		}
		servers = append(servers, value)
	}
	value := map[string]any{"servers": servers}
	if config.DNSFinal != "" {
		value["final"] = config.DNSFinal
	}
	return value
}

func singBoxExperimental(config servicesingbox.Config, clashAPISecret string, users, inboundTags []string, cacheFilePath string) map[string]any {
	if config.LogLevel == "" {
		config.ClashAPIEnabled, config.ClashAPIListen = true, "127.0.0.1:9090"
		config.V2RayAPIEnabled, config.V2RayAPIListen = true, "127.0.0.1:10000"
	}
	value := map[string]any{}
	if config.ClashAPIEnabled {
		clashAPI := map[string]any{"external_controller": config.ClashAPIListen}
		if clashAPISecret != "" {
			clashAPI["secret"] = clashAPISecret
		}
		value["clash_api"] = clashAPI
	}
	if config.V2RayAPIEnabled {
		value["v2ray_api"] = map[string]any{"listen": config.V2RayAPIListen, "stats": map[string]any{"enabled": true, "users": users, "inbounds": inboundTags}}
	}
	cachePath := cacheFilePath
	if cachePath == "" {
		cachePath = "cache.db"
	}
	value["cache_file"] = map[string]any{"enabled": true, "path": cachePath}
	if len(value) == 0 {
		return nil
	}
	return value
}

func vlessInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, paths CertificatePaths) map[string]any {
	entries := make([]any, 0, len(devices))
	flow := ""
	if isTCPTransport(protocol) {
		flow = "xtls-rprx-vision"
	}
	for _, device := range devices {
		entry := map[string]any{"name": device.DeviceID, "uuid": device.UUID}
		if flow != "" {
			entry["flow"] = flow
		}
		entries = append(entries, entry)
	}
	tls := map[string]any{"enabled": true, "server_name": setting(protocol, "serverName")}
	if setting(protocol, "tlsMode") == "certificate" {
		tls["certificate_path"] = paths.CertificatePath
		tls["key_path"] = paths.KeyPath
	} else {
		tls["reality"] = map[string]any{
			"enabled":     true,
			"handshake":   map[string]any{"server": setting(protocol, "serverName"), "server_port": numberSetting(protocol, "handshakeServerPort", 443)},
			"private_key": setting(protocol, "realityPrivateKey"), "short_id": []string{setting(protocol, "shortId")},
		}
	}
	value := map[string]any{
		"type": "vless", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"users": entries, "tls": tls,
	}
	if transport := vlessTransport(protocol); transport != nil {
		value["transport"] = transport
	}
	return value
}

func vlessTransport(protocol serviceprotocol.Protocol) map[string]any {
	switch setting(protocol, "transport") {
	case "ws":
		return map[string]any{"type": "ws", "path": setting(protocol, "wsPath")}
	case "grpc":
		return map[string]any{"type": "grpc", "service_name": setting(protocol, "grpcServiceName")}
	case "httpupgrade":
		return map[string]any{"type": "httpupgrade", "path": setting(protocol, "httpupgradePath")}
	case "http":
		return map[string]any{"type": "http", "path": setting(protocol, "httpPath")}
	default:
		return nil // tcp is the implicit default
	}
}

// isTCPTransport reports whether a VLESS protocol uses the default TCP transport
// (explicit "tcp" or an unset legacy value). The Vision flow is TCP-only.
func isTCPTransport(protocol serviceprotocol.Protocol) bool {
	switch setting(protocol, "transport") {
	case "", "<nil>", "tcp":
		return true
	default:
		return false
	}
}

func vmessInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, paths CertificatePaths) map[string]any {
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "uuid": device.UUID, "alterId": 0})
	}
	value := map[string]any{
		"type": "vmess", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"users": entries,
		"transport": map[string]any{
			"type":                   "ws",
			"path":                   setting(protocol, "wsPath"),
			"max_early_data":         numberSetting(protocol, "wsMaxEarlyData", 0),
			"early_data_header_name": setting(protocol, "wsEarlyDataHeaderName"),
		},
	}
	if setting(protocol, "tlsMode") == "certificate" {
		value["tls"] = map[string]any{
			"enabled":          true,
			"server_name":      setting(protocol, "serverName"),
			"certificate_path": paths.CertificatePath,
			"key_path":         paths.KeyPath,
		}
	}
	return value
}

func hysteriaInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, certificatePath, keyPath string) map[string]any {
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "password": device.Password})
	}
	return map[string]any{
		"type": "hysteria2", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"up_mbps": numberSetting(protocol, "upMbps", 100), "down_mbps": numberSetting(protocol, "downMbps", 100), "users": entries,
		"tls": map[string]any{"enabled": true, "server_name": setting(protocol, "serverName"), "alpn": []string{"h3"}, "certificate_path": certificatePath, "key_path": keyPath},
	}
}

func tuicInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, certificatePath, keyPath string) map[string]any {
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "uuid": device.UUID, "password": device.Password})
	}
	return map[string]any{
		"type": "tuic", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"users": entries, "congestion_control": setting(protocol, "congestionControl"),
		"tls": map[string]any{"enabled": true, "server_name": setting(protocol, "serverName"), "alpn": []string{"h3"}, "certificate_path": certificatePath, "key_path": keyPath},
	}
}

func anyTLSInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, certificatePath, keyPath string) map[string]any {
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "password": device.Password})
	}
	return map[string]any{
		"type": "anytls", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"users": entries,
		"tls":   map[string]any{"enabled": true, "server_name": setting(protocol, "serverName"), "certificate_path": certificatePath, "key_path": keyPath},
	}
}

func shadowsocksInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice) map[string]any {
	method := setting(protocol, "method")
	inbound := map[string]any{
		"type": "shadowsocks", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"network": "tcp", "method": method, "password": setting(protocol, "password"),
	}
	if !strings.HasPrefix(method, "2022-") {
		return inbound
	}
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "password": shadowsocksUserPassword(method, device.Password)})
	}
	inbound["users"] = entries
	return inbound
}

func trojanInbound(protocol serviceprotocol.Protocol, devices []serviceuser.ArtifactDevice, paths CertificatePaths) map[string]any {
	entries := make([]any, 0, len(devices))
	for _, device := range devices {
		entries = append(entries, map[string]any{"name": device.DeviceID, "password": device.Password})
	}
	tls := map[string]any{"enabled": true, "server_name": setting(protocol, "serverName")}
	if setting(protocol, "tlsMode") == "certificate" {
		tls["certificate_path"] = paths.CertificatePath
		tls["key_path"] = paths.KeyPath
	} else {
		tls["reality"] = map[string]any{
			"enabled":     true,
			"handshake":   map[string]any{"server": setting(protocol, "serverName"), "server_port": numberSetting(protocol, "handshakeServerPort", 443)},
			"private_key": setting(protocol, "realityPrivateKey"), "short_id": []string{setting(protocol, "shortId")},
		}
	}
	return map[string]any{
		"type": "trojan", "tag": protocol.InboundTag(), "listen": protocol.Listen, "listen_port": protocol.ListenPort,
		"users": entries, "tls": tls,
	}
}

// boundDevices returns the enabled devices of available users assigned to the
// protocol; each becomes one inbound user entry named by its device id.
func boundDevices(devices []serviceuser.ArtifactDevice, available func(string) bool, assignments map[string]map[string]bool, protocolID string) []serviceuser.ArtifactDevice {
	result := make([]serviceuser.ArtifactDevice, 0, len(devices))
	for _, device := range devices {
		if device.Enabled && available(device.UserID) && assignments[device.UserID][protocolID] {
			result = append(result, device)
		}
	}
	return result
}

var inputNow = func() time.Time { return time.Now().UTC() }
