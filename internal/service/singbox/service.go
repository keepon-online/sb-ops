package singbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

var builtinTags = map[string]bool{"direct": true, "block": true}

type Service struct {
	store *Store
	now   func() time.Time
	// warpRegisterURL 允许测试注入假注册端点；生产固定为 Cloudflare 客户端 API。
	warpRegisterURL string
	// ruleSets 供 dry-run 本地评估 rule_set 条件（按需下载 .srs 并缓存）。
	ruleSets *remoteRuleSetMatcher
}

func NewService(store *Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }, warpRegisterURL: defaultWarpRegisterURL, ruleSets: newRemoteRuleSetMatcher()}
}

func (s *Service) GetConfig(ctx context.Context) (Config, error) {
	if err := s.store.EnsureConfig(ctx, s.now()); err != nil {
		return Config{}, err
	}
	return s.store.GetConfig(ctx)
}

func (s *Service) ClashAPISecret(ctx context.Context) (string, error) {
	if err := s.store.EnsureConfig(ctx, s.now()); err != nil {
		return "", err
	}
	return s.store.ClashAPISecret(ctx)
}

// ClashAPIEndpoint resolves the local Clash API base URL and secret from the
// panel-managed sing-box configuration — the single source of truth for the
// embedded core. enabled is false when the Clash API is switched off in the
// panel. The returned endpoint always points at a local address.
func (s *Service) ClashAPIEndpoint(ctx context.Context) (endpoint, secret string, enabled bool, err error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return "", "", false, err
	}
	if !config.ClashAPIEnabled {
		return "", "", false, nil
	}
	endpoint, err = localHTTPEndpoint(config.ClashAPIListen)
	if err != nil {
		return "", "", false, err
	}
	secret, err = s.store.ClashAPISecret(ctx)
	return endpoint, secret, true, err
}

// EnsureClashAPISecret generates and persists a random Clash API secret when
// the API is enabled and no secret exists yet, so the local API is
// authenticated by default and the collector and connection probe share one
// credential. It is idempotent and safe under concurrent callers.
func (s *Service) EnsureClashAPISecret(ctx context.Context) (string, error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return "", err
	}
	if !config.ClashAPIEnabled {
		return "", nil
	}
	if existing, err := s.store.ClashAPISecret(ctx); err != nil {
		return "", err
	} else if existing != "" {
		return existing, nil
	}
	secretValue := randomHex(24)
	if err := s.store.SetClashAPISecret(ctx, secretValue, s.now()); err != nil {
		return "", err
	}
	return secretValue, nil
}

// localHTTPEndpoint converts a Clash API listen address into a local HTTP base
// URL. Wildcard listeners are rewritten to 127.0.0.1 because the collector and
// connection probe run on the same host as the embedded core.
func localHTTPEndpoint(listen string) (string, error) {
	hostPort, err := normalizeListenHost(listen)
	if err != nil {
		return "", err
	}
	return "http://" + hostPort, nil
}

// V2RayAPIEndpoint resolves the local V2Ray stats gRPC address from the
// panel-managed sing-box configuration. enabled is false when the V2Ray API is
// switched off in the panel.
func (s *Service) V2RayAPIEndpoint(ctx context.Context) (address string, enabled bool, err error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return "", false, err
	}
	if !config.V2RayAPIEnabled {
		return "", false, nil
	}
	address, err = normalizeListenHost(config.V2RayAPIListen)
	if err != nil {
		return "", false, err
	}
	return address, true, nil
}

// normalizeListenHost rewrites a wildcard listen address to 127.0.0.1 so the
// in-process collector and probes can reach the embedded core over loopback.
func normalizeListenHost(listen string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", err
	}
	if port == "" || port == "0" {
		return "", fmt.Errorf("监听端口无效")
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "*", "0.0.0.0", "::", "localhost":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

func (s *Service) UpdateConfig(ctx context.Context, input ConfigWriteInput) (Config, error) {
	if input.Revision < 1 {
		return Config{}, validation("revision", "must be positive")
	}
	value, fields := configFromInput(input)
	if len(fields) > 0 {
		return Config{}, &common.ValidationError{Fields: fields}
	}
	if err := s.validateOutboundReferences(ctx, value); err != nil {
		return Config{}, err
	}
	if exists, err := s.outboundExists(ctx, value.DefaultOutboundTag); err != nil {
		return Config{}, err
	} else if !exists {
		return Config{}, validation("defaultOutboundTag", "must reference an existing outbound")
	}
	if err := s.store.UpdateConfig(ctx, value, input.Revision, s.now(), input.ClashAPISecret); err != nil {
		return Config{}, err
	}
	return s.GetConfig(ctx)
}

func (s *Service) ListOutbounds(ctx context.Context) ([]Outbound, error) {
	values, err := s.store.ListOutbounds(ctx)
	if err != nil {
		return nil, err
	}
	for index := range values {
		values[index] = maskOutbound(values[index])
	}
	return values, nil
}

// GetOutbound 返回单个（脱敏）出站，更新 handler 用它取改前快照计算审计 diff。
func (s *Service) GetOutbound(ctx context.Context, id string) (Outbound, error) {
	value, err := s.store.GetOutbound(ctx, id)
	if err != nil {
		return Outbound{}, err
	}
	return maskOutbound(value), nil
}

// GetRule 返回单个服务器路由规则，更新 handler 用它取改前快照计算审计 diff。
func (s *Service) GetRule(ctx context.Context, id string) (RouteRule, error) {
	return s.store.GetRule(ctx, id)
}

func (s *Service) CreateOutbound(ctx context.Context, input OutboundWriteInput) (Outbound, error) {
	value, fields := outboundFromInput(input)
	if value.Type == OutboundTypeWarp && (value.Warp == nil || value.Warp.PrivateKey == "") {
		fields["warp.privateKey"] = "is required"
	}
	if len(fields) > 0 {
		return Outbound{}, &common.ValidationError{Fields: fields}
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateOutbound(ctx, value, input.Password); err != nil {
		return Outbound{}, err
	}
	created, err := s.store.GetOutbound(ctx, value.ID)
	return maskOutbound(created), err
}

func (s *Service) UpdateOutbound(ctx context.Context, input OutboundWriteInput) (Outbound, error) {
	if input.Revision < 1 {
		return Outbound{}, validation("revision", "must be positive")
	}
	value, fields := outboundFromInput(input)
	if len(fields) > 0 {
		return Outbound{}, &common.ValidationError{Fields: fields}
	}
	current, err := s.store.GetOutbound(ctx, input.ID)
	if err != nil {
		return Outbound{}, err
	}
	if current.Type != value.Type {
		return Outbound{}, validation("type", "cannot be changed after creation")
	}
	// WARP 私钥只在填写时更新：留空表示沿用已存值（与 Clash API 密钥语义一致）。
	if value.Warp != nil && value.Warp.PrivateKey == "" && current.Warp != nil {
		value.Warp.PrivateKey = current.Warp.PrivateKey
	}
	if value.Type == OutboundTypeWarp && (value.Warp == nil || value.Warp.PrivateKey == "") {
		return Outbound{}, validation("warp.privateKey", "is required")
	}
	if current.Tag != value.Tag {
		used, err := s.outboundReferenced(ctx, current.Tag)
		if err != nil {
			return Outbound{}, err
		}
		if used {
			return Outbound{}, common.ErrReferenced
		}
	}
	value.ID, value.CreatedAt, value.UpdatedAt = current.ID, current.CreatedAt, s.now()
	if err := s.store.UpdateOutbound(ctx, value, input.Revision, input.Password); err != nil {
		return Outbound{}, err
	}
	updated, err := s.store.GetOutbound(ctx, input.ID)
	return maskOutbound(updated), err
}

func (s *Service) DeleteOutbound(ctx context.Context, id string) error {
	value, err := s.store.GetOutbound(ctx, id)
	if err != nil {
		return err
	}
	used, err := s.outboundReferenced(ctx, value.Tag)
	if err != nil {
		return err
	}
	if used {
		return common.ErrReferenced
	}
	return s.store.DeleteOutbound(ctx, id)
}

func (s *Service) ListRules(ctx context.Context) ([]RouteRule, error) { return s.store.ListRules(ctx) }

func (s *Service) CreateRule(ctx context.Context, input RouteRuleWriteInput) (RouteRule, error) {
	value, fields := routeRuleFromInput(input)
	if len(fields) > 0 {
		return RouteRule{}, &common.ValidationError{Fields: fields}
	}
	if exists, err := s.outboundExists(ctx, value.OutboundTag); err != nil {
		return RouteRule{}, err
	} else if !exists {
		return RouteRule{}, validation("outboundTag", "must reference an existing outbound")
	}
	if err := s.validateRuleSetReferences(ctx, value.Conditions); err != nil {
		return RouteRule{}, err
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateRule(ctx, value); err != nil {
		return RouteRule{}, err
	}
	return s.store.GetRule(ctx, value.ID)
}

func (s *Service) UpdateRule(ctx context.Context, input RouteRuleWriteInput) (RouteRule, error) {
	if input.Revision < 1 {
		return RouteRule{}, validation("revision", "must be positive")
	}
	value, fields := routeRuleFromInput(input)
	if len(fields) > 0 {
		return RouteRule{}, &common.ValidationError{Fields: fields}
	}
	if exists, err := s.outboundExists(ctx, value.OutboundTag); err != nil {
		return RouteRule{}, err
	} else if !exists {
		return RouteRule{}, validation("outboundTag", "must reference an existing outbound")
	}
	if err := s.validateRuleSetReferences(ctx, value.Conditions); err != nil {
		return RouteRule{}, err
	}
	current, err := s.store.GetRule(ctx, input.ID)
	if err != nil {
		return RouteRule{}, err
	}
	value.ID, value.CreatedAt, value.UpdatedAt = current.ID, current.CreatedAt, s.now()
	if err := s.store.UpdateRule(ctx, value, input.Revision); err != nil {
		return RouteRule{}, err
	}
	return s.store.GetRule(ctx, input.ID)
}

func (s *Service) DeleteRule(ctx context.Context, id string) error {
	return s.store.DeleteRule(ctx, id)
}

func (s *Service) OrderRules(ctx context.Context, input OrderInput) ([]RouteRule, error) {
	// 完整性/去重校验由 store 层在事务内完成（同文案 ValidationError）。
	if err := s.store.OrderRules(ctx, input.IDs, s.now()); err != nil {
		return nil, err
	}
	return s.store.ListRules(ctx)
}

func (s *Service) ListRuleSets(ctx context.Context) ([]RuleSet, error) {
	return s.store.ListRuleSets(ctx)
}

func (s *Service) CreateRuleSet(ctx context.Context, input RuleSetWriteInput) (RuleSet, error) {
	value, fields := ruleSetFromInput(input)
	if len(fields) > 0 {
		return RuleSet{}, &common.ValidationError{Fields: fields}
	}
	if err := s.validateDetourReference(ctx, value.DownloadDetour); err != nil {
		return RuleSet{}, err
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateRuleSet(ctx, value); err != nil {
		return RuleSet{}, err
	}
	return s.store.GetRuleSet(ctx, value.ID)
}

func (s *Service) UpdateRuleSet(ctx context.Context, input RuleSetWriteInput) (RuleSet, error) {
	if input.Revision < 1 {
		return RuleSet{}, validation("revision", "must be positive")
	}
	value, fields := ruleSetFromInput(input)
	if len(fields) > 0 {
		return RuleSet{}, &common.ValidationError{Fields: fields}
	}
	if err := s.validateDetourReference(ctx, value.DownloadDetour); err != nil {
		return RuleSet{}, err
	}
	current, err := s.store.GetRuleSet(ctx, input.ID)
	if err != nil {
		return RuleSet{}, err
	}
	if !strings.EqualFold(current.Tag, value.Tag) {
		used, err := s.store.RuleSetReferenced(ctx, current.Tag)
		if err != nil {
			return RuleSet{}, err
		}
		if used {
			return RuleSet{}, common.ErrReferenced
		}
	}
	value.ID, value.CreatedAt, value.UpdatedAt = current.ID, current.CreatedAt, s.now()
	if err := s.store.UpdateRuleSet(ctx, value, input.Revision); err != nil {
		return RuleSet{}, err
	}
	return s.store.GetRuleSet(ctx, value.ID)
}

// validateDetourReference 校验规则集的 download_detour 指向存在的出站：
// 悬空的下载出站会在内核拉取远程规则集时才失败，启动期同步无从暴露。
func (s *Service) validateDetourReference(ctx context.Context, detour string) error {
	if detour == "" {
		return nil
	}
	if exists, err := s.outboundExists(ctx, detour); err != nil {
		return err
	} else if !exists {
		return validation("downloadDetour", "must reference an existing outbound")
	}
	return nil
}

func (s *Service) DeleteRuleSet(ctx context.Context, id string) error {
	value, err := s.store.GetRuleSet(ctx, id)
	if err != nil {
		return err
	}
	used, err := s.store.RuleSetReferenced(ctx, value.Tag)
	if err != nil {
		return err
	}
	if used {
		return common.ErrReferenced
	}
	return s.store.DeleteRuleSet(ctx, id)
}

func (s *Service) DryRun(ctx context.Context, input DryRunInput) (DryRunResult, error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return DryRunResult{}, err
	}
	if strings.TrimSpace(input.Host) == "" {
		return DryRunResult{}, validation("host", "is required")
	}
	rules, err := s.store.ListRules(ctx)
	if err != nil {
		return DryRunResult{}, err
	}
	ruleSets, err := s.store.ListRuleSets(ctx)
	if err != nil {
		return DryRunResult{}, err
	}
	urls := make(map[string]string, len(ruleSets))
	for _, ruleSet := range ruleSets {
		urls[ruleSet.Tag] = ruleSet.URL
	}
	return evaluate(ctx, s.ruleSets, urls, rules, config.DefaultOutboundTag, input), nil
}

func (s *Service) Validate(ctx context.Context) (ValidationResult, error) {
	config, err := s.GetConfig(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	rules, err := s.store.ListRules(ctx)
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{Valid: true, Issues: []ValidationIssue{}}
	if exists, err := s.outboundExists(ctx, config.DefaultOutboundTag); err != nil {
		return ValidationResult{}, err
	} else if !exists {
		result.Valid = false
		result.Issues = append(result.Issues, ValidationIssue{Level: "error", Message: "default outbound does not exist"})
	}
	for _, server := range config.DNSServers {
		if server.Detour == "" {
			continue
		}
		exists, err := s.outboundExists(ctx, server.Detour)
		if err != nil {
			return ValidationResult{}, err
		}
		if !exists {
			result.Valid = false
			result.Issues = append(result.Issues, ValidationIssue{Level: "error", Message: fmt.Sprintf("DNS server %q references an unknown detour", server.Tag)})
		}
	}
	catchAll := false
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if catchAll {
			result.Issues = append(result.Issues, ValidationIssue{Level: "warning", RuleID: rule.ID, Message: "rule is unreachable after a catch-all rule"})
		}
		if emptyConditions(rule.Conditions) {
			catchAll = true
		}
	}
	return result, nil
}

func (s *Service) outboundExists(ctx context.Context, tag string) (bool, error) {
	if builtinTags[tag] {
		return true, nil
	}
	values, err := s.store.ListOutbounds(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(values, func(value Outbound) bool { return value.Tag == tag }), nil
}

func (s *Service) validateRuleSetReferences(ctx context.Context, conditions RouteConditions) error {
	if len(conditions.RuleSets) == 0 {
		return nil
	}
	values, err := s.store.ListRuleSets(ctx)
	if err != nil {
		return err
	}
	for _, tag := range conditions.RuleSets {
		if !slices.ContainsFunc(values, func(value RuleSet) bool { return value.Tag == tag }) {
			return validation("conditions.ruleSets", "must reference an existing rule set")
		}
	}
	return nil
}

func (s *Service) validateOutboundReferences(ctx context.Context, value Config) error {
	for index, server := range value.DNSServers {
		if server.Detour == "" {
			continue
		}
		exists, err := s.outboundExists(ctx, server.Detour)
		if err != nil {
			return err
		}
		if !exists {
			return validation(fmt.Sprintf("dnsServers.%d.detour", index), "must reference an existing outbound")
		}
	}
	return nil
}

func (s *Service) outboundReferenced(ctx context.Context, tag string) (bool, error) {
	used, err := s.store.OutboundReferenced(ctx, tag)
	if err != nil || used {
		return used, err
	}
	config, err := s.GetConfig(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(config.DNSServers, func(server DNSServer) bool { return server.Detour == tag }), nil
}

func configFromInput(input ConfigWriteInput) (Config, map[string]string) {
	fields := map[string]string{}
	value := Config{LogLevel: strings.ToLower(strings.TrimSpace(input.LogLevel)), LogTimestamp: input.LogTimestamp, DNSServers: normalizeDNSServers(input.DNSServers), DNSFinal: strings.TrimSpace(input.DNSFinal), ClashAPIEnabled: input.ClashAPIEnabled, ClashAPIListen: strings.TrimSpace(input.ClashAPIListen), V2RayAPIEnabled: input.V2RayAPIEnabled, V2RayAPIListen: strings.TrimSpace(input.V2RayAPIListen), DefaultOutboundTag: strings.TrimSpace(input.DefaultOutboundTag)}
	if !slices.Contains([]string{"trace", "debug", "info", "warn", "error", "fatal", "panic"}, value.LogLevel) {
		fields["logLevel"] = "is invalid"
	}
	if value.ClashAPIEnabled && !validListen(value.ClashAPIListen) {
		fields["clashApiListen"] = "must be host:port"
	}
	if value.V2RayAPIEnabled && !validListen(value.V2RayAPIListen) {
		fields["v2rayApiListen"] = "must be host:port"
	}
	if value.DefaultOutboundTag == "" {
		fields["defaultOutboundTag"] = "is required"
	}
	tags := map[string]bool{}
	for index, server := range value.DNSServers {
		prefix := fmt.Sprintf("dnsServers.%d", index)
		if server.Tag == "" || tags[server.Tag] {
			fields[prefix+".tag"] = "must be unique and non-empty"
		}
		tags[server.Tag] = true
		if strings.TrimSpace(server.Address) == "" {
			fields[prefix+".address"] = "is required"
		}
	}
	if value.DNSFinal != "" && !tags[value.DNSFinal] {
		fields["dnsFinal"] = "must reference a DNS server tag"
	}
	return value, fields
}

func outboundFromInput(input OutboundWriteInput) (Outbound, map[string]string) {
	fields := map[string]string{}
	value := Outbound{ID: input.ID, Tag: strings.TrimSpace(input.Tag), Type: strings.ToLower(strings.TrimSpace(input.Type)), Server: strings.TrimSpace(input.Server), ServerPort: input.ServerPort, Username: strings.TrimSpace(input.Username)}
	if value.Type == "" {
		value.Type = OutboundTypeSOCKS
	}
	if value.Tag == "" || builtinTags[value.Tag] {
		fields["tag"] = "must be a non-reserved outbound tag"
	}
	switch value.Type {
	case OutboundTypeSOCKS:
		if value.Server == "" {
			fields["server"] = "is required"
		}
		if value.ServerPort < 1 || value.ServerPort > 65535 {
			fields["serverPort"] = "must be between 1 and 65535"
		}
	case OutboundTypeWarp:
		value.Warp = normalizeWarpSettings(input.Warp)
		validateWarpSettings(value.Warp, fields)
	default:
		fields["type"] = "must be socks or warp"
	}
	return value, fields
}

func normalizeWarpSettings(settings *WarpSettings) *WarpSettings {
	if settings == nil {
		return nil
	}
	value := *settings
	value.Endpoint = strings.TrimSpace(value.Endpoint)
	if value.Endpoint == "" {
		value.Endpoint = DefaultWarpEndpoint
	}
	value.PublicKey = strings.TrimSpace(value.PublicKey)
	value.PrivateKey = strings.TrimSpace(value.PrivateKey)
	value.IPv4 = strings.TrimSpace(value.IPv4)
	value.IPv6 = strings.TrimSpace(value.IPv6)
	value.PrivateKeySet = false
	return &value
}

func validateWarpSettings(settings *WarpSettings, fields map[string]string) {
	if settings == nil {
		fields["warp"] = "is required"
		return
	}
	if _, _, err := net.SplitHostPort(settings.Endpoint); err != nil {
		fields["warp.endpoint"] = "must be host:port"
	}
	if !validWireGuardKey(settings.PublicKey) {
		fields["warp.publicKey"] = "must be a base64 32-byte key"
	}
	if settings.PrivateKey != "" && !validWireGuardKey(settings.PrivateKey) {
		fields["warp.privateKey"] = "must be a base64 32-byte key"
	}
	if prefix, err := netip.ParsePrefix(settings.IPv4); err != nil || !prefix.Addr().Is4() {
		fields["warp.ipv4"] = "must be an IPv4 CIDR"
	}
	if settings.IPv6 != "" {
		if _, err := netip.ParsePrefix(settings.IPv6); err != nil {
			fields["warp.ipv6"] = "must be an IPv6 CIDR"
		}
	}
	if length := len(settings.Reserved); length != 0 && length != 3 {
		fields["warp.reserved"] = "must contain 3 bytes"
	}
	for _, byteValue := range settings.Reserved {
		if byteValue < 0 || byteValue > 255 {
			fields["warp.reserved"] = "must contain 3 bytes"
			break
		}
	}
	if settings.MTU != 0 && (settings.MTU < 512 || settings.MTU > 65535) {
		fields["warp.mtu"] = "must be between 512 and 65535"
	}
}

// validWireGuardKey checks for a base64-encoded 32-byte curve25519 key.
func validWireGuardKey(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(raw) == 32
}

// maskOutbound strips the WARP private key for API responses, replacing it
// with a presence flag so the frontend can offer「留空保持原值」.
func maskOutbound(value Outbound) Outbound {
	if value.Warp == nil {
		return value
	}
	masked := *value.Warp
	masked.PrivateKeySet = masked.PrivateKey != ""
	masked.PrivateKey = ""
	value.Warp = &masked
	return value
}

func routeRuleFromInput(input RouteRuleWriteInput) (RouteRule, map[string]string) {
	fields := map[string]string{}
	value := RouteRule{ID: input.ID, Name: strings.TrimSpace(input.Name), Enabled: input.Enabled, Priority: input.Priority, Conditions: normalizeConditions(input.Conditions), OutboundTag: strings.TrimSpace(input.OutboundTag)}
	if value.Name == "" {
		fields["name"] = "is required"
	}
	if value.Priority < 0 {
		fields["priority"] = "must not be negative"
	}
	if value.OutboundTag == "" {
		fields["outboundTag"] = "is required"
	}
	for _, cidr := range value.Conditions.IPCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			fields["conditions.ipCidrs"] = "contains an invalid CIDR"
		}
	}
	for _, port := range value.Conditions.Ports {
		if port < 1 || port > 65535 {
			fields["conditions.ports"] = "must contain ports between 1 and 65535"
		}
	}
	for _, network := range value.Conditions.Networks {
		if network != "tcp" && network != "udp" {
			fields["conditions.networks"] = "must contain tcp or udp"
		}
	}
	for _, code := range value.Conditions.GeoIP {
		if !validGeoCode(code) {
			fields["conditions.geoip"] = "must contain lowercase country/region codes"
		}
	}
	for _, code := range value.Conditions.GeoSite {
		if !validGeoCode(code) {
			fields["conditions.geosite"] = "must contain lowercase category codes"
		}
	}
	return value, fields
}

func ruleSetFromInput(input RuleSetWriteInput) (RuleSet, map[string]string) {
	fields := map[string]string{}
	value := RuleSet{ID: input.ID, Tag: strings.ToLower(strings.TrimSpace(input.Tag)), Format: strings.ToLower(strings.TrimSpace(input.Format)), URL: strings.TrimSpace(input.URL), DownloadDetour: strings.TrimSpace(input.DownloadDetour)}
	if value.Tag == "" {
		fields["tag"] = "is required"
	}
	if value.Format != "source" && value.Format != "binary" {
		fields["format"] = "must be source or binary"
	}
	if value.URL == "" {
		fields["url"] = "is required"
	} else if parsed, parseErr := url.Parse(value.URL); parseErr != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		// 远程规则集由内嵌内核在服务端拉取——必须是带主机的 http(s) URL，
		// 防止把内核当内网探测跳板（gopher/file 等协议内核也不支持）。
		fields["url"] = "must be an http(s) URL"
	}
	return value, fields
}

func normalizeDNSServers(values []DNSServer) []DNSServer {
	out := make([]DNSServer, 0, len(values))
	for _, value := range values {
		out = append(out, DNSServer{Tag: strings.TrimSpace(value.Tag), Address: strings.TrimSpace(value.Address), Detour: strings.TrimSpace(value.Detour)})
	}
	return out
}

func normalizeConditions(value RouteConditions) RouteConditions {
	value.Domains = normalizeStrings(value.Domains)
	value.DomainSuffixes = normalizeStrings(value.DomainSuffixes)
	value.IPCIDRs = normalizeStrings(value.IPCIDRs)
	value.Networks = normalizeStrings(value.Networks)
	value.InboundTags = normalizeStrings(value.InboundTags)
	value.GeoIP = normalizeGeoValues(value.GeoIP, "geoip")
	value.GeoSite = normalizeGeoValues(value.GeoSite, "geosite")
	value.RuleSets = normalizeStrings(value.RuleSets)
	slices.Sort(value.Ports)
	value.Ports = slices.Compact(value.Ports)
	return value
}

func normalizeStrings(values []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			out, seen[value] = append(out, value), true
		}
	}
	slices.Sort(out)
	return out
}

// normalizeGeoValues lowercases and dedupes geo codes, stripping an optional
// "geoip:"/"geosite:" prefix so Clash-style values can be pasted as-is.
func normalizeGeoValues(values []string, prefix string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.TrimPrefix(value, prefix+":")
		if value != "" && !seen[value] {
			out, seen[value] = append(out, value), true
		}
	}
	slices.Sort(out)
	return out
}

func validGeoCode(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validListen(value string) bool {
	_, _, err := net.SplitHostPort(value)
	return err == nil
}

func emptyConditions(value RouteConditions) bool {
	return len(value.Domains)+len(value.DomainSuffixes)+len(value.IPCIDRs)+len(value.Ports)+len(value.Networks)+len(value.InboundTags)+len(value.GeoIP)+len(value.GeoSite)+len(value.RuleSets) == 0
}

func validation(field, message string) *common.ValidationError {
	return &common.ValidationError{Fields: map[string]string{field: message}}
}
