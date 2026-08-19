package singbox

import (
	"context"
	"net/netip"
	"strings"
)

// evaluate walks the rules top-down like the kernel router. rule_set conditions
// are matched locally by downloading the referenced rule sets; geoip/geosite
// conditions still defer to the kernel and mark the rule unknown.
func evaluate(ctx context.Context, matcher *remoteRuleSetMatcher, ruleSetURLs map[string]string, rules []RouteRule, defaultOutbound string, input DryRunInput) DryRunResult {
	result := DryRunResult{Trace: make([]Evaluation, 0, len(rules)), OutboundTag: defaultOutbound}
	for index := range rules {
		rule := rules[index]
		if !rule.Enabled {
			result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "skipped", Reason: "rule disabled"})
			continue
		}
		matched, unknown, reason := evaluateRule(ctx, matcher, ruleSetURLs, rule.Conditions, input)
		if unknown {
			result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "unknown", Reason: reason})
			continue
		}
		evaluation := Evaluation{RuleID: rule.ID, Name: rule.Name, Matched: matched, Status: "miss", Reason: reason}
		if matched {
			evaluation.Status, evaluation.OutboundTag = "match", rule.OutboundTag
			result.Trace = append(result.Trace, evaluation)
			result.MatchedRule, result.OutboundTag = &rule, rule.OutboundTag
			return result
		}
		result.Trace = append(result.Trace, evaluation)
	}
	return result
}

// evaluateRule returns (matched, unknown, reason). unknown=true means the rule
// could not be evaluated locally (geoip/geosite, or rule-set fetch failure).
func evaluateRule(ctx context.Context, matcher *remoteRuleSetMatcher, ruleSetURLs map[string]string, conditions RouteConditions, input DryRunInput) (bool, bool, string) {
	host := strings.ToLower(strings.TrimSpace(input.Host))
	if len(conditions.Domains) > 0 && !containsString(conditions.Domains, host) {
		return false, false, "domain did not match"
	}
	if len(conditions.DomainSuffixes) > 0 {
		matched := false
		for _, suffix := range conditions.DomainSuffixes {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				matched = true
				break
			}
		}
		if !matched {
			return false, false, "domain suffix did not match"
		}
	}
	if len(conditions.IPCIDRs) > 0 {
		address, err := netip.ParseAddr(host)
		if err != nil {
			return false, false, "host is not an IP address"
		}
		matched := false
		for _, value := range conditions.IPCIDRs {
			prefix, _ := netip.ParsePrefix(value)
			if prefix.Contains(address) {
				matched = true
				break
			}
		}
		if !matched {
			return false, false, "IP CIDR did not match"
		}
	}
	if len(conditions.Ports) > 0 && !containsInt(conditions.Ports, input.Port) {
		return false, false, "port did not match"
	}
	if len(conditions.Networks) > 0 && !containsString(conditions.Networks, input.Network) {
		return false, false, "network did not match"
	}
	if len(conditions.InboundTags) > 0 && !containsString(conditions.InboundTags, input.InboundTag) {
		return false, false, "inbound tag did not match"
	}
	if len(conditions.RuleSets) > 0 {
		matched := false
		for _, tag := range conditions.RuleSets {
			url, ok := ruleSetURLs[tag]
			if !ok {
				return false, true, "引用的规则集 " + tag + " 不存在"
			}
			hit, err := matcher.matchHost(ctx, url, host)
			if err != nil {
				return false, true, "规则集 " + tag + " " + err.Error()
			}
			if hit {
				matched = true
				break // rule_set 列表内是 OR 语义，命中一个即可
			}
		}
		if !matched {
			return false, false, "rule set did not match"
		}
	}
	if len(conditions.GeoIP)+len(conditions.GeoSite) > 0 {
		return false, true, "geoip/geosite condition requires the live kernel to evaluate"
	}
	return true, false, "all conditions matched"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
