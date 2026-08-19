package singbox

import (
	"errors"
	"reflect"
	"testing"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

func TestApplyAIUnlockTemplate(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: OutboundTypeWarp, Tag: "warp",
		Warp: &WarpSettings{PublicKey: testWarpKey('B'), PrivateKey: testWarpKey('A'), IPv4: "172.16.0.2/32"}}); err != nil {
		t.Fatalf("create warp outbound: %v", err)
	}
	// 删掉一个种子规则集，模板应能自愈重建。
	sets, err := service.ListRuleSets(t.Context())
	if err != nil {
		t.Fatalf("list rule sets: %v", err)
	}
	for _, set := range sets {
		if set.Tag == "gemini" {
			if err := service.DeleteRuleSet(t.Context(), set.ID); err != nil {
				t.Fatalf("delete gemini rule set: %v", err)
			}
		}
	}
	rule, err := service.ApplyAIUnlockTemplate(t.Context(), "warp")
	if err != nil {
		t.Fatalf("apply template: %v", err)
	}
	if rule.Name != AIUnlockRuleName || rule.OutboundTag != "warp" || !rule.Enabled {
		t.Fatalf("rule = %#v", rule)
	}
	if !reflect.DeepEqual(rule.Conditions.RuleSets, []string{"anthropic", "gemini", "openai"}) {
		t.Fatalf("rule sets = %#v", rule.Conditions.RuleSets)
	}
	if rule.Priority != 10 {
		t.Fatalf("priority = %d, want 10 on empty rule table", rule.Priority)
	}
	sets, err = service.ListRuleSets(t.Context())
	if err != nil {
		t.Fatalf("list rule sets after template: %v", err)
	}
	tags := map[string]bool{}
	for _, set := range sets {
		tags[set.Tag] = true
	}
	if !tags["gemini"] || !tags["anthropic"] || !tags["openai"] {
		t.Fatalf("AI rule sets missing after template: %#v", tags)
	}
	// 重复应用：更新既有规则而不是新建，出站可切换。
	if _, err := service.CreateOutbound(t.Context(), OutboundWriteInput{Type: OutboundTypeSOCKS, Tag: "clean", Server: "127.0.0.1", ServerPort: 1080}); err != nil {
		t.Fatalf("create second outbound: %v", err)
	}
	updated, err := service.ApplyAIUnlockTemplate(t.Context(), "clean")
	if err != nil {
		t.Fatalf("re-apply template: %v", err)
	}
	if updated.ID != rule.ID || updated.OutboundTag != "clean" || updated.Priority != rule.Priority {
		t.Fatalf("updated rule = %#v", updated)
	}
	rules, err := service.ListRules(t.Context())
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("rules = %d, want 1 (template must upsert)", len(rules))
	}
	// 未知出站被拒绝。
	if _, err := service.ApplyAIUnlockTemplate(t.Context(), "missing"); !errors.As(err, new(*common.ValidationError)) {
		t.Fatalf("unknown outbound err = %v, want validation error", err)
	}
}
