package policy

import "testing"

// TestRuleFromInputRejectsInexpressibleConditions：端口/网络条件不支持与
// 其他条件组合、应用层协议条件无对应规则类型——写入期拒绝，防止 dry-run
// 与 clash.yaml 实际产物静默分叉（条件类型间是 AND 语义，经典规则无法跨
// 类型组合）。
func TestRuleFromInputRejectsInexpressibleConditions(t *testing.T) {
	cases := []struct {
		name       string
		conditions Conditions
		wantField  string
	}{
		{
			name:       "protocols",
			conditions: Conditions{Domains: []string{"a.example.com"}, Protocols: []string{"http"}},
			wantField:  "conditions.protocols",
		},
		{
			name:       "ports combined with domains",
			conditions: Conditions{Domains: []string{"a.example.com"}, Ports: []int{443}},
			wantField:  "conditions",
		},
		{
			name:       "ports combined with networks",
			conditions: Conditions{Ports: []int{443}, Networks: []string{"udp"}},
			wantField:  "conditions",
		},
		{
			name:       "networks combined with rule sets",
			conditions: Conditions{RuleSets: []string{"ads"}, Networks: []string{"tcp"}},
			wantField:  "conditions",
		},
		{
			name:       "invalid network value",
			conditions: Conditions{Networks: []string{"sctp"}},
			wantField:  "conditions.networks",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, fields := ruleFromInput(RuleWriteInput{Name: "rule", Action: "direct", Conditions: testCase.conditions})
			if _, ok := fields[testCase.wantField]; !ok {
				t.Fatalf("fields = %v, want %q", fields, testCase.wantField)
			}
		})
	}
}

// TestRuleFromInputAcceptsSingleTypeConditions：单条件的端口/网络规则是
// 可编译形态，不应被组合校验误伤。
func TestRuleFromInputAcceptsSingleTypeConditions(t *testing.T) {
	for _, conditions := range []Conditions{
		{Ports: []int{443, 8443}},
		{Networks: []string{"udp"}},
		{Domains: []string{"a.example.com"}, DomainSuffixes: []string{"example.org"}},
	} {
		if _, fields := ruleFromInput(RuleWriteInput{Name: "rule", Action: "direct", Conditions: conditions}); len(fields) != 0 {
			t.Fatalf("conditions %#v rejected: %v", conditions, fields)
		}
	}
}

// TestPolicyFromInputRejectsUnknownAction：非法动作入库会让之后每一次订阅
// 编译失败（clashAction 报错、全站同步 409），写入期以 fieldErrors 拒绝。
func TestPolicyFromInputRejectsUnknownAction(t *testing.T) {
	_, fields := policyFromInput(PolicyWriteInput{Name: "policy", DefaultAction: "invalid"})
	if _, ok := fields["defaultAction"]; !ok {
		t.Fatalf("fields = %v, want defaultAction", fields)
	}
	_, fields = ruleFromInput(RuleWriteInput{Name: "rule", Action: "invalid"})
	if _, ok := fields["action"]; !ok {
		t.Fatalf("fields = %v, want action", fields)
	}
}
