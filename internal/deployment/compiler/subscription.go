package compiler

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"
	"gopkg.in/yaml.v3"
)

func clashSubscription(input Input, protocols []serviceprotocol.Protocol, user serviceuser.ArtifactUser, device serviceuser.ArtifactDevice) ([]byte, error) {
	proxies := make([]any, 0, len(protocols))
	names := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		if !protocol.Enabled || !input.UserProtocols[user.ID][protocol.ID] {
			continue
		}
		name := protocol.Name
		names = append(names, name)
		switch protocol.Type {
		case serviceprotocol.TypeVLESS:
			proxy := map[string]any{
				"name": name, "type": "vless", "server": input.PublicAddress, "port": protocol.ListenPort,
				"uuid": device.UUID, "tls": true, "udp": true,
				"servername": setting(protocol, "serverName"), "client-fingerprint": "chrome",
			}
			switch setting(protocol, "transport") {
			case "ws":
				proxy["network"] = "ws"
				proxy["ws-opts"] = map[string]any{"path": setting(protocol, "wsPath")}
			case "grpc":
				proxy["network"] = "grpc"
				proxy["grpc-opts"] = map[string]any{"grpc-service-name": setting(protocol, "grpcServiceName")}
			case "httpupgrade":
				proxy["network"] = "ws"
				proxy["ws-opts"] = map[string]any{"path": setting(protocol, "httpupgradePath"), "v2ray-http-upgrade": true}
			case "http":
				proxy["network"] = "http"
				proxy["http-opts"] = map[string]any{"method": "GET", "path": []string{setting(protocol, "httpPath")}}
			default:
				proxy["network"] = "tcp"
				proxy["flow"] = "xtls-rprx-vision"
			}
			if setting(protocol, "tlsMode") == "reality" {
				proxy["reality-opts"] = map[string]any{"public-key": setting(protocol, "realityPublicKey"), "short-id": setting(protocol, "shortId")}
			}
			proxies = append(proxies, proxy)
		case serviceprotocol.TypeVMess:
			proxies = append(proxies, map[string]any{
				"name": name, "type": "vmess", "server": input.PublicAddress, "port": protocol.ListenPort,
				"uuid": device.UUID, "alterId": 0, "cipher": "auto", "network": "ws", "tls": false, "udp": true,
				"ws-opts": map[string]any{
					"path": setting(protocol, "wsPath"), "max-early-data": numberSetting(protocol, "wsMaxEarlyData", 0),
					"early-data-header-name": setting(protocol, "wsEarlyDataHeaderName"),
				},
			})
		case serviceprotocol.TypeHysteria2:
			proxies = append(proxies, map[string]any{
				"name": name, "type": "hysteria2", "server": input.PublicAddress, "port": protocol.ListenPort,
				"password": device.Password, "sni": setting(protocol, "serverName"), "alpn": []string{"h3"}, "skip-cert-verify": true,
				"up":   fmt.Sprintf("%d Mbps", numberSetting(protocol, "upMbps", 100)),
				"down": fmt.Sprintf("%d Mbps", numberSetting(protocol, "downMbps", 100)),
			})
		case serviceprotocol.TypeTUIC:
			proxies = append(proxies, map[string]any{
				"name": name, "type": "tuic", "server": input.PublicAddress, "port": protocol.ListenPort,
				"uuid": device.UUID, "password": device.Password, "sni": setting(protocol, "serverName"), "alpn": []string{"h3"},
				"congestion-controller": setting(protocol, "congestionControl"), "udp-relay-mode": "native", "skip-cert-verify": true,
			})
		case serviceprotocol.TypeAnyTLS:
			proxies = append(proxies, map[string]any{
				"name": name, "type": "anytls", "server": input.PublicAddress, "port": protocol.ListenPort,
				"password": device.Password, "sni": setting(protocol, "serverName"), "udp": true, "skip-cert-verify": true,
			})
		case serviceprotocol.TypeShadowsocks:
			proxies = append(proxies, map[string]any{
				"name": name, "type": "ss", "server": input.PublicAddress, "port": protocol.ListenPort,
				"cipher": setting(protocol, "method"), "password": shadowsocksSubscriptionPassword(protocol, device), "udp": false,
			})
		case serviceprotocol.TypeTrojan:
			proxy := map[string]any{
				"name": name, "type": "trojan", "server": input.PublicAddress, "port": protocol.ListenPort,
				"password": device.Password, "udp": true, "sni": setting(protocol, "serverName"),
				"client-fingerprint": "chrome",
			}
			if setting(protocol, "tlsMode") == "reality" {
				proxy["reality-opts"] = map[string]any{"public-key": setting(protocol, "realityPublicKey"), "short-id": setting(protocol, "shortId")}
			} else {
				proxy["skip-cert-verify"] = true
			}
			proxies = append(proxies, proxy)
		}
	}
	rules, err := clashRules(input, user)
	if err != nil {
		return nil, err
	}
	argoProxies, argoNames := argoProxyNodes(input, protocols, user, device)
	proxies = append(proxies, argoProxies...)
	names = append(names, argoNames...)
	document := map[string]any{
		"port":                7890,
		"socks-port":          7891,
		"allow-lan":           false,
		"mode":                "rule",
		"log-level":           "info",
		"dns":                 clashDNS(),
		"geodata-mode":        true,
		"geo-auto-update":     true,
		"geo-update-interval": 24,
		"proxies":             proxies,
		"proxy-groups":        clashProxyGroups(names),
		"rule-providers":      clashRuleProviders(),
		"rules":               rules,
	}
	raw, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// argoProxyNodes renders one extra vmess node per running Argo tunnel: the
// carried vmess inbound reached through the Cloudflare edge (TLS terminates
// at the edge, origin is plain WebSocket). preferred_address overrides the
// dialed server for CDN-optimized fronting; Host/SNI always stay on the
// tunnel domain.
func argoProxyNodes(input Input, protocols []serviceprotocol.Protocol, user serviceuser.ArtifactUser, device serviceuser.ArtifactDevice) ([]any, []string) {
	byID := make(map[string]serviceprotocol.Protocol, len(protocols))
	for _, protocol := range protocols {
		byID[protocol.ID] = protocol
	}
	proxies := []any{}
	names := []string{}
	seenProtocols := map[string]bool{}
	for _, tunnel := range input.ArgoTunnels {
		if !tunnel.Enabled || tunnel.AssignedDomain == "" {
			continue
		}
		protocol, ok := byID[tunnel.ProtocolID]
		if !ok || !protocol.Enabled || !input.UserProtocols[user.ID][protocol.ID] {
			continue
		}
		if protocol.Type != serviceprotocol.TypeVMess || seenProtocols[protocol.ID] {
			// 同协议第二条隧道会产出重名节点（clash 配置非法）——存储层的
			// 唯一索引已阻止，这里兜底跳过。
			continue
		}
		seenProtocols[protocol.ID] = true
		server := tunnel.PreferredAddress
		if server == "" {
			server = tunnel.AssignedDomain
		}
		name := protocol.Name + " Argo"
		proxies = append(proxies, map[string]any{
			"name": name, "type": "vmess", "server": server, "port": 443,
			"uuid": device.UUID, "alterId": 0, "cipher": "auto", "network": "ws",
			"tls": true, "servername": tunnel.AssignedDomain, "udp": false,
			"ws-opts": map[string]any{
				"path": setting(protocol, "wsPath"), "headers": map[string]any{"Host": tunnel.AssignedDomain},
				"max-early-data":         numberSetting(protocol, "wsMaxEarlyData", 0),
				"early-data-header-name": setting(protocol, "wsEarlyDataHeaderName"),
			},
		})
		names = append(names, name)
	}
	return proxies, names
}

const (
	clashProxyGroup     = "🚀 节点选择"
	clashAutoGroup      = "♻️ 自动选择"
	clashAdsGroup       = "🛑 广告拦截"
	clashAIGroup        = "🤖 AI 服务"
	clashBilibiliGroup  = "📺 哔哩哔哩"
	clashYouTubeGroup   = "📹 油管视频"
	clashGoogleGroup    = "🔍 谷歌服务"
	clashPrivateGroup   = "🏠 私有网络"
	clashChinaGroup     = "🔒 国内服务"
	clashTelegramGroup  = "📲 电报消息"
	clashGitHubGroup    = "🐱 Github"
	clashMicrosoftGroup = "Ⓜ️ 微软服务"
	clashAppleGroup     = "🍏 苹果服务"
	clashSocialGroup    = "🌐 社交媒体"
	clashStreamingGroup = "🎬 流媒体"
	clashEducationGroup = "📚 教育资源"
	clashServiceGroup   = "🔀 代理服务"
	clashFinalGroup     = "🐟 漏网之鱼"
)

func clashDNS() map[string]any {
	chinaResolvers := []string{"https://120.53.53.53/dns-query", "https://223.5.5.5/dns-query"}
	// 非中国域名用国内可达的公共 DoH（DNSPod/AliDNS），避免 cloudflare/google
	// 这类在境内被墙的解析器导致所有非 CN 域名解析失败。
	foreignResolvers := []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"}
	return map[string]any{
		"enable": true, "enhanced-mode": "fake-ip", "ipv6": true,
		"nameserver": chinaResolvers,
		"nameserver-policy": map[string]any{
			"geosite:cn,private":      chinaResolvers,
			"geosite:geolocation-!cn": foreignResolvers,
		},
		"proxy-server-nameserver": chinaResolvers,
		"respect-rules":           true,
	}
}

func clashProxyGroups(names []string) []any {
	selectorTargets := append([]string{"DIRECT", "REJECT", clashAutoGroup}, names...)
	autoTargets := append([]string(nil), names...)
	if len(autoTargets) == 0 {
		autoTargets = []string{"DIRECT"}
	}
	categoryTargets := append([]string{clashProxyGroup, "DIRECT", "REJECT", clashAutoGroup}, names...)
	groups := []any{
		map[string]any{"name": clashProxyGroup, "type": "select", "proxies": selectorTargets},
		map[string]any{"name": clashAutoGroup, "type": "url-test", "proxies": autoTargets, "url": "https://www.gstatic.com/generate_204", "interval": 300, "tolerance": 50},
		map[string]any{"name": clashAdsGroup, "type": "select", "proxies": append([]string{"REJECT", "DIRECT", clashProxyGroup, clashAutoGroup}, names...)},
		map[string]any{"name": clashAIGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashBilibiliGroup, "type": "select", "proxies": append([]string{"DIRECT", clashProxyGroup, clashAutoGroup}, names...)},
		map[string]any{"name": clashYouTubeGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashGoogleGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashPrivateGroup, "type": "select", "proxies": []string{"DIRECT", clashProxyGroup}},
		map[string]any{"name": clashChinaGroup, "type": "select", "proxies": []string{"DIRECT", clashProxyGroup}},
		map[string]any{"name": clashTelegramGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashGitHubGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashMicrosoftGroup, "type": "select", "proxies": append([]string{"DIRECT", clashProxyGroup, clashAutoGroup}, names...)},
		map[string]any{"name": clashAppleGroup, "type": "select", "proxies": append([]string{"DIRECT", clashProxyGroup, clashAutoGroup}, names...)},
		map[string]any{"name": clashSocialGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashStreamingGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashEducationGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashServiceGroup, "type": "select", "proxies": categoryTargets},
		map[string]any{"name": clashFinalGroup, "type": "select", "proxies": categoryTargets},
	}
	return groups
}

func clashRuleProviders() map[string]any {
	sources := servicepolicy.ClashRuleSetSources()
	providers := make(map[string]any, len(sources))
	for name, source := range sources {
		providers[name] = map[string]any{
			"type": "http", "behavior": source.Behavior, "format": source.Format, "url": source.URL,
			"path": "./ruleset/" + name + "." + source.Format, "interval": 86400, "proxy": clashProxyGroup,
		}
	}
	return providers
}

func clashRules(input Input, user serviceuser.ArtifactUser) ([]string, error) {
	policies := append([]servicepolicy.Policy(nil), input.Policies...)
	sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
	rules := []string{}
	defaultAction := "Proxy"
	for _, policy := range policies {
		if !policy.Enabled || !input.UserPolicies[user.ID][policy.ID] {
			continue
		}
		action, err := clashAction(policy.DefaultAction)
		if err != nil {
			return nil, fmt.Errorf("策略 %s 默认动作: %w", policy.Name, err)
		}
		defaultAction = action
		for _, rule := range input.PolicyRules[policy.ID] {
			if !rule.Enabled {
				continue
			}
			compiled, err := clashPolicyRule(rule)
			if err != nil {
				return nil, fmt.Errorf("策略规则 %s: %w", rule.Name, err)
			}
			rules = append(rules, compiled...)
		}
	}
	if len(rules) == 0 {
		rules = defaultClashRules()
		return rules, nil
	}
	return append(rules, "MATCH,"+defaultAction), nil
}

func defaultClashRules() []string {
	return []string{
		"RULE-SET,category-ads-all," + clashAdsGroup,
		"DOMAIN-SUFFIX,local,DIRECT",
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,224.0.0.0/4,DIRECT,no-resolve",
		"IP-CIDR6,::1/128,DIRECT,no-resolve",
		"IP-CIDR6,fc00::/7,DIRECT,no-resolve",
		"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
		"RULE-SET,private," + clashPrivateGroup + ",no-resolve",
		"RULE-SET,category-ai-!cn," + clashAIGroup,
		"RULE-SET,openai," + clashAIGroup,
		"RULE-SET,claude," + clashAIGroup,
		"RULE-SET,bilibili," + clashBilibiliGroup,
		"RULE-SET,biliintl," + clashBilibiliGroup,
		"RULE-SET,youtube," + clashYouTubeGroup,
		"RULE-SET,google," + clashGoogleGroup,
		"RULE-SET,google-ip," + clashGoogleGroup + ",no-resolve",
		"RULE-SET,github," + clashGitHubGroup,
		"RULE-SET,gitlab," + clashGitHubGroup,
		"RULE-SET,telegram," + clashTelegramGroup,
		"RULE-SET,telegram-ip," + clashTelegramGroup + ",no-resolve",
		"RULE-SET,microsoft," + clashMicrosoftGroup,
		"RULE-SET,apple," + clashAppleGroup,
		"RULE-SET,applemusic," + clashAppleGroup,
		"RULE-SET,facebook," + clashSocialGroup,
		"RULE-SET,instagram," + clashSocialGroup,
		"RULE-SET,twitter," + clashSocialGroup,
		"RULE-SET,tiktok," + clashSocialGroup,
		"RULE-SET,linkedin," + clashSocialGroup,
		"RULE-SET,netflix," + clashStreamingGroup,
		"RULE-SET,disney," + clashStreamingGroup,
		"RULE-SET,hulu," + clashStreamingGroup,
		"RULE-SET,hbo," + clashStreamingGroup,
		"RULE-SET,amazon," + clashStreamingGroup,
		"RULE-SET,bahamut," + clashStreamingGroup,
		"RULE-SET,spotify," + clashStreamingGroup,
		"RULE-SET,coursera," + clashEducationGroup,
		"RULE-SET,edx," + clashEducationGroup,
		"RULE-SET,udemy," + clashEducationGroup,
		"RULE-SET,khanacademy," + clashEducationGroup,
		"RULE-SET,category-scholar-!cn," + clashEducationGroup,
		"RULE-SET,geolocation-cn," + clashChinaGroup,
		"RULE-SET,cn-domains," + clashChinaGroup,
		"RULE-SET,cn," + clashChinaGroup + ",no-resolve",
		"RULE-SET,proxy," + clashServiceGroup,
		"MATCH," + clashFinalGroup,
	}
}

func clashPolicyRule(rule servicepolicy.Rule) ([]string, error) {
	action, err := clashAction(rule.Action)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(rule.Conditions.RuleSets)+len(rule.Conditions.Domains)+len(rule.Conditions.DomainSuffixes)+len(rule.Conditions.IPCIDRs))
	providers := clashRuleProviders()
	for _, name := range rule.Conditions.RuleSets {
		provider, ok := providers[name].(map[string]any)
		if !ok {
			// Unknown/retired rule-set name (e.g. a provider renamed upstream):
			// skip it instead of failing the whole subscription compile, so a
			// stale reference does not block edits. The dry-run matcher treats
			// such names as empty sets for parity.
			continue
		}
		compiled := "RULE-SET," + name + "," + clashRuleSetAction(name, action)
		if rule.NoResolve && provider["behavior"] == "ipcidr" {
			compiled += ",no-resolve"
		}
		result = append(result, compiled)
	}
	for _, domain := range rule.Conditions.Domains {
		result = append(result, "DOMAIN,"+domain+","+action)
	}
	for _, suffix := range rule.Conditions.DomainSuffixes {
		result = append(result, "DOMAIN-SUFFIX,"+suffix+","+action)
	}
	for _, cidr := range rule.Conditions.IPCIDRs {
		kind := "IP-CIDR"
		if strings.Contains(cidr, ":") {
			kind = "IP-CIDR6"
		}
		value := kind + "," + cidr + "," + action
		if rule.NoResolve {
			value += ",no-resolve"
		}
		result = append(result, value)
	}
	return result, nil
}

func clashRuleSetAction(name, action string) string {
	if action == "REJECT" && name == "category-ads-all" {
		return clashAdsGroup
	}
	if action != clashProxyGroup {
		return action
	}
	switch name {
	case "category-ai-!cn", "openai", "claude":
		return clashAIGroup
	case "bilibili", "biliintl":
		return clashBilibiliGroup
	case "youtube":
		return clashYouTubeGroup
	case "google", "google-ip":
		return clashGoogleGroup
	case "private":
		return clashPrivateGroup
	case "geolocation-cn", "cn-domains", "cn":
		return clashChinaGroup
	case "github", "gitlab":
		return clashGitHubGroup
	case "telegram", "telegram-ip":
		return clashTelegramGroup
	case "microsoft":
		return clashMicrosoftGroup
	case "apple", "applemusic":
		return clashAppleGroup
	case "facebook", "instagram", "twitter", "tiktok", "linkedin":
		return clashSocialGroup
	case "netflix", "disney", "hulu", "hbo", "amazon", "bahamut", "spotify":
		return clashStreamingGroup
	case "coursera", "edx", "udemy", "khanacademy", "category-scholar-!cn":
		return clashEducationGroup
	case "proxy", "steam":
		return clashServiceGroup
	default:
		return action
	}
}

func clashAction(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "direct":
		return "DIRECT", nil
	case "proxy":
		return clashProxyGroup, nil
	case "block":
		return "REJECT", nil
	default:
		return "", fmt.Errorf("不支持的动作 %q", value)
	}
}

func shadowsocksSubscriptionPassword(protocol serviceprotocol.Protocol, device serviceuser.ArtifactDevice) string {
	method := setting(protocol, "method")
	if !strings.HasPrefix(method, "2022-") {
		return setting(protocol, "password")
	}
	// SS-2022 多用户模式下，内核按「服务端密钥 + 各用户密钥」校验，
	// 客户端密码必须是 "服务端密钥:用户密钥" 的冒号组合，只给用户密钥
	// 会始终认证失败（invalid request）。
	return setting(protocol, "password") + ":" + shadowsocksUserPassword(method, device.Password)
}

func shadowsocksUserPassword(method, credential string) string {
	digest := sha256.Sum256([]byte(credential))
	keyLength := 32
	if strings.Contains(method, "aes-128") {
		keyLength = 16
	}
	return base64.StdEncoding.EncodeToString(digest[:keyLength])
}
