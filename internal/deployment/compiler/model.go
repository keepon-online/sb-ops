package compiler

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"

	serviceargo "github.com/keepon-online/sb-ops/internal/service/argo"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
)

var (
	ErrPublicAddressNotConfigured = errors.New("公网地址未配置")
	hostnamePattern               = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type Input struct {
	PublicAddress       string
	PanelDomain         string
	SubscriptionURLBase string
	TLSCertPath         string
	TLSKeyPath          string
	CertificatePaths    map[string]CertificatePaths
	Protocols           []serviceprotocol.Protocol
	Users               []serviceuser.ArtifactUser
	Devices             []serviceuser.ArtifactDevice
	UserProtocols       map[string]map[string]bool
	Policies            []servicepolicy.Policy
	PolicyRules         map[string][]servicepolicy.Rule
	UserPolicies        map[string]map[string]bool
	SingBoxConfig       servicesingbox.Config
	ClashAPISecret      string
	Outbounds           []servicesingbox.Outbound
	OutboundPasswords   map[string]string
	RouteRules          []servicesingbox.RouteRule
	RuleSets            []servicesingbox.RuleSet
	ArgoTunnels         []serviceargo.Tunnel
	CacheFilePath       string
	AllowPrivateAddress bool
}

type CertificatePaths struct {
	CertificatePath string
	KeyPath         string
}

// Subscription is one per-device clash.yaml artifact.
type Subscription struct {
	UserID   string
	DeviceID string
	Content  []byte
}

type Result struct {
	ServerConfig  []byte
	Subscriptions []Subscription
}

func (i Input) Validate() error {
	if i.AllowPrivateAddress {
		if !validAddress(i.PublicAddress) {
			return fmt.Errorf("公网地址必须是 IPv4 或域名")
		}
	} else if !validPublicAddress(i.PublicAddress) {
		return fmt.Errorf("公网地址必须是公网 IPv4 或域名")
	}
	if err := i.validateSingBoxConfig(); err != nil {
		return err
	}
	seen := map[string]string{}
	for _, value := range i.Protocols {
		if !value.Enabled {
			continue
		}
		key := transport(value.Type) + ":" + fmt.Sprint(value.ListenPort)
		if owner, ok := seen[key]; ok {
			return fmt.Errorf("协议 %s 与 %s 使用相同的 %s 监听", owner, value.Name, key)
		}
		seen[key] = value.Name
		switch value.Type {
		case serviceprotocol.TypeVLESS:
			switch setting(value, "tlsMode") {
			case "reality":
				if err := requireRealityKeys(value); err != nil {
					return err
				}
				if port := numberSetting(value, "handshakeServerPort", 443); port < 1 || port > 65535 {
					return fmt.Errorf("协议 %s 的 Reality 回退端口无效", value.Name)
				}
			case "certificate":
				if err := i.validateTLSPaths(value); err != nil {
					return err
				}
			default:
				return fmt.Errorf("协议 %s 必须使用 Reality 或证书", value.Name)
			}
		case serviceprotocol.TypeVMess:
			if setting(value, "transport") != "ws" || setting(value, "wsPath") == "" {
				return fmt.Errorf("协议 %s 必须使用 WebSocket 并配置路径", value.Name)
			}
			switch setting(value, "tlsMode") {
			case "", "<nil>", "none":
			case "certificate":
				if err := i.validateTLSPaths(value); err != nil {
					return err
				}
			default:
				return fmt.Errorf("协议 %s 的 TLS 模式无效", value.Name)
			}
		case serviceprotocol.TypeHysteria2, serviceprotocol.TypeTUIC, serviceprotocol.TypeAnyTLS:
			if err := i.validateTLSPaths(value); err != nil {
				return err
			}
		case serviceprotocol.TypeTrojan:
			switch setting(value, "tlsMode") {
			case "reality":
				if err := requireRealityKeys(value); err != nil {
					return err
				}
			case "certificate":
				if err := i.validateTLSPaths(value); err != nil {
					return err
				}
			default:
				return fmt.Errorf("协议 %s 必须使用 Reality 或证书", value.Name)
			}
		case serviceprotocol.TypeShadowsocks:
			for _, key := range []string{"method", "password"} {
				if setting(value, key) == "" {
					return fmt.Errorf("协议 %s 缺少 %s", value.Name, key)
				}
			}
		default:
			return fmt.Errorf("单 VPS 编译器不支持协议类型 %s", value.Type)
		}
	}
	return nil
}

func (i Input) validateTLSPaths(protocol serviceprotocol.Protocol) error {
	if protocol.CertificateID != nil {
		if paths, ok := i.CertificatePaths[*protocol.CertificateID]; ok && strings.TrimSpace(paths.CertificatePath) != "" && strings.TrimSpace(paths.KeyPath) != "" {
			return nil
		}
	}
	if strings.TrimSpace(i.TLSCertPath) == "" || strings.TrimSpace(i.TLSKeyPath) == "" {
		return fmt.Errorf("%s 需要配置 TLS 证书和密钥路径", protocol.Type)
	}
	return nil
}

func (i Input) validateSingBoxConfig() error {
	config := i.SingBoxConfig
	if config.LogLevel == "" {
		config = servicesingbox.Config{
			LogLevel: "info", ClashAPIEnabled: true, ClashAPIListen: "127.0.0.1:9090",
			V2RayAPIEnabled: true, V2RayAPIListen: "127.0.0.1:10000", DefaultOutboundTag: "direct",
		}
	}
	if !validLogLevel(config.LogLevel) {
		return fmt.Errorf("sing-box 日志级别 %q 无效", config.LogLevel)
	}
	if config.ClashAPIEnabled && !validListenAddress(config.ClashAPIListen) {
		return fmt.Errorf("监听地址无效（Clash API）")
	}
	if config.V2RayAPIEnabled && !validListenAddress(config.V2RayAPIListen) {
		return fmt.Errorf("V2Ray API 监听地址无效")
	}
	tags := map[string]bool{"direct": true, "block": true}
	for _, outbound := range i.Outbounds {
		if outbound.Tag == "" || tags[outbound.Tag] {
			return fmt.Errorf("出站标签 %q 无效或重复", outbound.Tag)
		}
		tags[outbound.Tag] = true
		switch outbound.Type {
		case servicesingbox.OutboundTypeSOCKS:
			if strings.TrimSpace(outbound.Server) == "" || outbound.ServerPort < 1 || outbound.ServerPort > 65535 {
				return fmt.Errorf("出站 %s 无效", outbound.Tag)
			}
		case servicesingbox.OutboundTypeWarp:
			if outbound.Warp == nil || outbound.Warp.PrivateKey == "" || outbound.Warp.PublicKey == "" || outbound.Warp.IPv4 == "" || outbound.Warp.Endpoint == "" {
				return fmt.Errorf("出站 %s 无效", outbound.Tag)
			}
		default:
			return fmt.Errorf("出站 %s 类型无效", outbound.Tag)
		}
	}
	if !tags[config.DefaultOutboundTag] {
		return fmt.Errorf("默认出站 %q 不存在", config.DefaultOutboundTag)
	}
	dnsTags := map[string]bool{}
	for _, server := range config.DNSServers {
		if server.Tag == "" || server.Address == "" || dnsTags[server.Tag] {
			return fmt.Errorf("DNS 服务器无效或重复")
		}
		dnsTags[server.Tag] = true
		if server.Detour != "" && !tags[server.Detour] {
			return fmt.Errorf("DNS detour %q 不存在", server.Detour)
		}
	}
	if config.DNSFinal != "" && !dnsTags[config.DNSFinal] {
		return fmt.Errorf("DNS final %q 不存在", config.DNSFinal)
	}
	ruleSetTags := map[string]bool{}
	for _, ruleSet := range i.RuleSets {
		if ruleSet.Tag == "" || ruleSetTags[ruleSet.Tag] {
			return fmt.Errorf("规则集标签无效或重复")
		}
		ruleSetTags[ruleSet.Tag] = true
		if ruleSet.Format != "source" && ruleSet.Format != "binary" {
			return fmt.Errorf("规则集 %s 的格式 %q 无效", ruleSet.Tag, ruleSet.Format)
		}
		if strings.TrimSpace(ruleSet.URL) == "" {
			return fmt.Errorf("规则集 %s 缺少下载地址", ruleSet.Tag)
		}
		if ruleSet.DownloadDetour != "" && !tags[ruleSet.DownloadDetour] {
			return fmt.Errorf("规则集 %s 的下载出站 %q 不存在", ruleSet.Tag, ruleSet.DownloadDetour)
		}
	}
	for _, rule := range i.RouteRules {
		if rule.Enabled && !tags[rule.OutboundTag] {
			return fmt.Errorf("路由规则 %s 引用了未知出站 %q", rule.Name, rule.OutboundTag)
		}
		for _, cidr := range rule.Conditions.IPCIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				return fmt.Errorf("路由规则 %s 的 CIDR %q 无效", rule.Name, cidr)
			}
		}
		for _, port := range rule.Conditions.Ports {
			if port < 1 || port > 65535 {
				return fmt.Errorf("路由规则 %s 的端口无效", rule.Name)
			}
		}
		for _, network := range rule.Conditions.Networks {
			if network != "tcp" && network != "udp" {
				return fmt.Errorf("路由规则 %s 的网络类型 %q 无效", rule.Name, network)
			}
		}
		for _, tag := range rule.Conditions.RuleSets {
			if !ruleSetTags[tag] {
				return fmt.Errorf("路由规则 %s 引用了未知规则集 %q", rule.Name, tag)
			}
		}
	}
	return nil
}

func validLogLevel(value string) bool {
	for _, level := range []string{"trace", "debug", "info", "warn", "error", "fatal", "panic"} {
		if value == level {
			return true
		}
	}
	return false
}

func validListenAddress(value string) bool {
	_, _, err := net.SplitHostPort(value)
	return err == nil
}

func (i Input) tlsPaths(protocol serviceprotocol.Protocol) CertificatePaths {
	if protocol.CertificateID != nil {
		if paths, ok := i.CertificatePaths[*protocol.CertificateID]; ok {
			return paths
		}
	}
	return CertificatePaths{CertificatePath: i.TLSCertPath, KeyPath: i.TLSKeyPath}
}

func validPublicAddress(value string) bool {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Is4() && !address.IsUnspecified() && !address.IsLoopback() && !address.IsPrivate()
	}
	return len(value) <= 253 && hostnamePattern.MatchString(value)
}

// validAddress is the development-mode variant: it accepts any IPv4 or a
// hostname, including private and loopback addresses, so the panel can be
// tested against a LAN IP.
func validAddress(value string) bool {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Is4() && !address.IsUnspecified()
	}
	return len(value) <= 253 && hostnamePattern.MatchString(value)
}

// requireRealityKeys 校验 Reality 协议的必填设置项（vless/trojan 共用）。
func requireRealityKeys(value serviceprotocol.Protocol) error {
	for _, key := range []string{"serverName", "realityPrivateKey", "realityPublicKey", "shortId"} {
		if setting(value, key) == "" {
			return fmt.Errorf("协议 %s 缺少 %s", value.Name, key)
		}
	}
	return nil
}

func setting(value serviceprotocol.Protocol, key string) string {
	if value.Settings[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value.Settings[key]))
}

func numberSetting(value serviceprotocol.Protocol, key string, fallback int) int {
	switch number := value.Settings[key].(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return fallback
	}
}

func transport(kind serviceprotocol.Type) string {
	if kind == serviceprotocol.TypeHysteria2 || kind == serviceprotocol.TypeTUIC {
		return "udp"
	}
	return "tcp"
}
