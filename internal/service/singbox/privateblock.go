package singbox

import (
	"context"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

// PrivateBlockRuleName is the reserved route rule name of the built-in
// "拒绝私网与回环" default. Re-seeding updates this rule in place.
const PrivateBlockRuleName = "拒绝私网与回环"

// privateNetworkCIDRs covers loopback, private ranges, link-local and
// unspecified addresses (IPv4+IPv6) that must never be dialed through the
// proxy inbounds.
var privateNetworkCIDRs = []string{
	"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7",
}

// EnsurePrivateBlockRule creates (or heals) the default「拒绝私网与回环」
// route rule pointing at the built-in block outbound. Called at startup so
// every deployment gets the safeguard; a user-deleted rule is re-created on
// the next boot (same semantics as the default rule-set seeding).
func (s *Service) EnsurePrivateBlockRule(ctx context.Context) (RouteRule, error) {
	rules, err := s.store.ListRules(ctx)
	if err != nil {
		return RouteRule{}, err
	}
	for _, rule := range rules {
		if rule.Name != PrivateBlockRuleName {
			continue
		}
		if rule.Enabled && rule.OutboundTag == "block" && len(rule.Conditions.IPCIDRs) == len(privateNetworkCIDRs) {
			return rule, nil
		}
		value, fields := routeRuleFromInput(RouteRuleWriteInput{ID: rule.ID, Name: rule.Name, Enabled: true, Priority: rule.Priority, Conditions: RouteConditions{IPCIDRs: privateNetworkCIDRs}, OutboundTag: "block"})
		if len(fields) > 0 {
			return RouteRule{}, &common.ValidationError{Fields: fields}
		}
		value.ID, value.CreatedAt = rule.ID, rule.CreatedAt
		if err := s.store.UpdateRule(ctx, value, rule.Revision); err != nil {
			return RouteRule{}, err
		}
		return s.store.GetRule(ctx, rule.ID)
	}
	priority := 5
	for _, rule := range rules {
		if rule.Priority >= priority {
			priority = rule.Priority + 5
		}
	}
	now := s.now()
	value, fields := routeRuleFromInput(RouteRuleWriteInput{Name: PrivateBlockRuleName, Enabled: true, Priority: priority, Conditions: RouteConditions{IPCIDRs: privateNetworkCIDRs}, OutboundTag: "block"})
	if len(fields) > 0 {
		return RouteRule{}, &common.ValidationError{Fields: fields}
	}
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateRule(ctx, value); err != nil {
		return RouteRule{}, err
	}
	return s.store.GetRule(ctx, value.ID)
}
