package singbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

// CanonicalAIRuleSets lists the rule sets behind the one-click AI unlock
// template (official SagerNet sing-geosite binaries). It is shared by the
// migration seeding and the template application so the two never drift.
var CanonicalAIRuleSets = []struct{ Tag, URL string }{
	{Tag: "openai", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-openai.srs"},
	{Tag: "gemini", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google-gemini.srs"},
	{Tag: "anthropic", URL: "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-anthropic.srs"},
}

// AIRuleSetTags returns just the tags of CanonicalAIRuleSets.
func AIRuleSetTags() []string {
	tags := make([]string, 0, len(CanonicalAIRuleSets))
	for _, ruleSet := range CanonicalAIRuleSets {
		tags = append(tags, ruleSet.Tag)
	}
	return tags
}

// AIUnlockRuleName is the reserved route rule name owned by the one-click AI
// unlock template. Re-applying the template updates this rule in place.
const AIUnlockRuleName = "AI 解锁"

// ApplyAIUnlockTemplate creates (or updates) the「AI 解锁」route rule that
// sends OpenAI/Gemini/Anthropic traffic through the given outbound — the
// panel equivalent of the warp-chain unlock shipped by the reference scripts.
// Missing canonical rule sets are re-created so a deleted library heals.
func (s *Service) ApplyAIUnlockTemplate(ctx context.Context, outboundTag string) (RouteRule, error) {
	outboundTag = strings.TrimSpace(outboundTag)
	if outboundTag == "" {
		return RouteRule{}, validation("outboundTag", "is required")
	}
	if exists, err := s.outboundExists(ctx, outboundTag); err != nil {
		return RouteRule{}, err
	} else if !exists {
		return RouteRule{}, validation("outboundTag", "must reference an existing outbound")
	}
	if err := s.ensureAIRuleSets(ctx); err != nil {
		return RouteRule{}, err
	}
	conditions := RouteConditions{RuleSets: AIRuleSetTags()}
	rules, err := s.store.ListRules(ctx)
	if err != nil {
		return RouteRule{}, err
	}
	for _, rule := range rules {
		if rule.Name != AIUnlockRuleName {
			continue
		}
		value, fields := routeRuleFromInput(RouteRuleWriteInput{ID: rule.ID, Name: rule.Name, Enabled: true, Priority: rule.Priority, Conditions: conditions, OutboundTag: outboundTag})
		if len(fields) > 0 {
			return RouteRule{}, &common.ValidationError{Fields: fields}
		}
		value.ID, value.CreatedAt = rule.ID, rule.CreatedAt
		if err := s.store.UpdateRule(ctx, value, rule.Revision); err != nil {
			return RouteRule{}, err
		}
		return s.store.GetRule(ctx, rule.ID)
	}
	priority := 10
	for _, rule := range rules {
		if rule.Priority >= priority {
			priority = rule.Priority + 10
		}
	}
	now := s.now()
	value, fields := routeRuleFromInput(RouteRuleWriteInput{Name: AIUnlockRuleName, Enabled: true, Priority: priority, Conditions: conditions, OutboundTag: outboundTag})
	if len(fields) > 0 {
		return RouteRule{}, &common.ValidationError{Fields: fields}
	}
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateRule(ctx, value); err != nil {
		return RouteRule{}, err
	}
	return s.store.GetRule(ctx, value.ID)
}

// ensureAIRuleSets re-creates any canonical AI rule set that was deleted.
func (s *Service) ensureAIRuleSets(ctx context.Context) error {
	existing, err := s.store.ListRuleSets(ctx)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, ruleSet := range existing {
		present[ruleSet.Tag] = true
	}
	now := s.now()
	for _, canonical := range CanonicalAIRuleSets {
		if present[canonical.Tag] {
			continue
		}
		value := RuleSet{ID: fmt.Sprintf("seed-%s", canonical.Tag), Tag: canonical.Tag, Format: "binary", URL: canonical.URL, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := s.store.CreateRuleSet(ctx, value); err != nil {
			return err
		}
	}
	return nil
}
