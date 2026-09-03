package singbox

import (
	"reflect"
	"testing"
)

func TestEnsurePrivateBlockRule(t *testing.T) {
	service, _ := newTestService(t)
	ctx := t.Context()

	rule, err := service.EnsurePrivateBlockRule(ctx)
	if err != nil {
		t.Fatalf("ensure rule: %v", err)
	}
	if rule.Name != PrivateBlockRuleName || rule.OutboundTag != "block" || !rule.Enabled {
		t.Fatalf("rule = %#v", rule)
	}
	if !reflect.DeepEqual(rule.Conditions.IPCIDRs, privateNetworkCIDRs) {
		t.Fatalf("cidrs = %#v", rule.Conditions.IPCIDRs)
	}

	// 幂等：重复调用返回既有规则，不新建。
	again, err := service.EnsurePrivateBlockRule(ctx)
	if err != nil {
		t.Fatalf("re-ensure rule: %v", err)
	}
	if again.ID != rule.ID || again.Revision != rule.Revision {
		t.Fatalf("idempotent ensure = %#v, want same rule %#v", again, rule)
	}

	// 自愈：规则被改动/停用后重置回默认。
	if _, err := service.UpdateRule(ctx, RouteRuleWriteInput{ID: rule.ID, Revision: rule.Revision, Name: rule.Name, Enabled: false, Priority: rule.Priority, Conditions: rule.Conditions, OutboundTag: "direct"}); err != nil {
		t.Fatalf("mutate rule: %v", err)
	}
	healed, err := service.EnsurePrivateBlockRule(ctx)
	if err != nil {
		t.Fatalf("heal rule: %v", err)
	}
	if healed.ID != rule.ID || !healed.Enabled || healed.OutboundTag != "block" {
		t.Fatalf("healed rule = %#v", healed)
	}

	// 重建：用户删除后重启补回，优先级避开被占用值。
	if err := service.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	if _, err := service.CreateRule(ctx, RouteRuleWriteInput{Name: "占位", Enabled: true, Priority: 5, Conditions: RouteConditions{IPCIDRs: []string{"127.0.0.0/8"}}, OutboundTag: "block"}); err != nil {
		t.Fatalf("occupy priority 5: %v", err)
	}
	recreated, err := service.EnsurePrivateBlockRule(ctx)
	if err != nil {
		t.Fatalf("recreate rule: %v", err)
	}
	if recreated.ID == rule.ID || recreated.Priority == 5 {
		t.Fatalf("recreated rule = %#v, want new id and free priority", recreated)
	}
}
