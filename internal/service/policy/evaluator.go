package policy

import (
	"slices"

	"context"
	"net/netip"
	"strings"
)

func Evaluate(rules []Rule, defaultAction string, input DryRunInput, matcher RuleSetMatcher) DryRunResult {
	result := DryRunResult{Trace: make([]Evaluation, 0, len(rules)), Action: defaultAction, Determined: true}
	ctx := context.Background()
	for index := range rules {
		rule := rules[index]
		if !rule.Enabled {
			result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "skipped", Reason: "rule disabled"})
			continue
		}
		localConditions := rule.Conditions
		localConditions.RuleSets = nil
		matched, reason := matches(localConditions, input)
		if !matched {
			result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "miss", Reason: reason})
			continue
		}
		if len(rule.Conditions.RuleSets) > 0 {
			ruleSetMatched, ruleSetReason, determined := matchRuleSets(ctx, matcher, rule.Conditions.RuleSets, input.Host)
			if !determined {
				result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "client", Reason: ruleSetReason})
				result.Determined = false
				return result
			}
			if !ruleSetMatched {
				result.Trace = append(result.Trace, Evaluation{RuleID: rule.ID, Name: rule.Name, Status: "miss", Reason: ruleSetReason})
				continue
			}
			evaluation := Evaluation{RuleID: rule.ID, Name: rule.Name, Matched: true, Status: "match", Reason: ruleSetReason, Action: rule.Action}
			result.Trace = append(result.Trace, evaluation)
			result.MatchedRule, result.Action = &rule, rule.Action
			return result
		}
		evaluation := Evaluation{RuleID: rule.ID, Name: rule.Name, Matched: true, Status: "match", Reason: reason, Action: rule.Action}
		result.Trace = append(result.Trace, evaluation)
		result.MatchedRule, result.Action = &rule, rule.Action
		return result
	}
	return result
}

// matchRuleSets evaluates a rule's RULE-SET conditions as an OR group (any
// matching provider counts as a match, mirroring how the compiler emits one
// RULE-SET line per provider). It returns:
//   - (true, "rule set matched", true) when any provider matches;
//   - (false, "rule set did not match", true) when every provider is locally
//     matchable and none matches — the caller continues to the next rule;
//   - (false, reason, false) when at least one provider cannot be evaluated
//     locally (unsupported format such as classical YAML, or a download/parse
//     failure) — the caller defers to the Clash client.
func matchRuleSets(ctx context.Context, matcher RuleSetMatcher, ruleSets []string, host string) (matched bool, reason string, determined bool) {
	if matcher == nil {
		return false, "rule set membership is evaluated by the Clash client", false
	}
	for _, name := range ruleSets {
		result := matcher.Match(ctx, name, host)
		if !result.Supported {
			return false, "rule set format is not locally matchable", false
		}
		if result.DownloadErr != nil {
			return false, "rule set download failed", false
		}
		if result.Matched {
			return true, "rule set matched", true
		}
	}
	return false, "rule set did not match", true
}

func matches(conditions Conditions, input DryRunInput) (bool, string) {
	host := strings.ToLower(strings.TrimSpace(input.Host))
	if len(conditions.Domains) > 0 && !containsString(conditions.Domains, host) {
		return false, "domain did not match"
	}
	if len(conditions.DomainSuffixes) > 0 {
		matched := false
		for _, suffix := range conditions.DomainSuffixes {
			suffix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(suffix), "."))
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				matched = true
				break
			}
		}
		if !matched {
			return false, "domain suffix did not match"
		}
	}
	if len(conditions.IPCIDRs) > 0 {
		address, err := netip.ParseAddr(host)
		if err != nil {
			return false, "host is not an IP address"
		}
		matched := false
		for _, value := range conditions.IPCIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err == nil && prefix.Contains(address) {
				matched = true
				break
			}
		}
		if !matched {
			return false, "IP CIDR did not match"
		}
	}
	if len(conditions.Ports) > 0 && !slices.Contains(conditions.Ports, input.Port) {
		return false, "port did not match"
	}
	if len(conditions.Networks) > 0 && !containsString(conditions.Networks, strings.ToLower(input.Network)) {
		return false, "network did not match"
	}
	if len(conditions.Protocols) > 0 && !containsString(conditions.Protocols, strings.ToLower(input.Protocol)) {
		return false, "protocol did not match"
	}
	return true, "all conditions matched"
}

func containsString(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == target {
			return true
		}
	}
	return false
}
