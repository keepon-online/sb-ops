package policy

import (
	"context"
	"testing"
)

type mockRuleSetMatcher struct {
	results map[string]RuleSetMatchResult
}

func (m *mockRuleSetMatcher) Match(_ context.Context, name, _ string) RuleSetMatchResult {
	if r, ok := m.results[name]; ok {
		return r
	}
	return RuleSetMatchResult{Supported: false}
}

func TestEvaluateContinuesPastRuleSetWhenKnownConditionsMiss(t *testing.T) {
	matcher := &mockRuleSetMatcher{results: map[string]RuleSetMatchResult{
		"openai": {Matched: true, Supported: true},
	}}
	result := Evaluate([]Rule{
		{ID: "rule-set", Name: "Rule set", Enabled: true, Action: "proxy", Conditions: Conditions{RuleSets: []string{"openai"}, Domains: []string{"api.openai.com"}}},
		{ID: "fallback", Name: "Fallback", Enabled: true, Action: "direct"},
	}, "block", DryRunInput{Host: "example.com"}, matcher)

	if !result.Determined || result.Action != "direct" || result.MatchedRule == nil || result.MatchedRule.ID != "fallback" {
		t.Fatalf("result = %#v, want determined fallback match", result)
	}
	if got := result.Trace[0].Status; got != "miss" {
		t.Fatalf("first status = %q, want miss", got)
	}
}

func TestEvaluateMatchesWhenRuleSetProviderMatches(t *testing.T) {
	matcher := &mockRuleSetMatcher{results: map[string]RuleSetMatchResult{
		"openai": {Matched: true, Supported: true},
	}}
	result := Evaluate([]Rule{
		{ID: "rule-set", Name: "Rule set", Enabled: true, Action: "proxy", Conditions: Conditions{RuleSets: []string{"openai"}, Domains: []string{"api.openai.com"}}},
		{ID: "fallback", Name: "Fallback", Enabled: true, Action: "direct"},
	}, "block", DryRunInput{Host: "api.openai.com"}, matcher)

	if !result.Determined || result.MatchedRule == nil || result.MatchedRule.ID != "rule-set" || result.Action != "proxy" {
		t.Fatalf("result = %#v, want determined rule-set match", result)
	}
	if got := result.Trace[0].Status; got != "match" {
		t.Fatalf("first status = %q, want match", got)
	}
}

func TestEvaluateContinuesWhenRuleSetProviderDoesNotMatch(t *testing.T) {
	matcher := &mockRuleSetMatcher{results: map[string]RuleSetMatchResult{
		"openai": {Matched: false, Supported: true},
	}}
	result := Evaluate([]Rule{
		{ID: "rule-set", Name: "Rule set", Enabled: true, Action: "proxy", Conditions: Conditions{RuleSets: []string{"openai"}, Domains: []string{"api.openai.com"}}},
		{ID: "fallback", Name: "Fallback", Enabled: true, Action: "direct"},
	}, "block", DryRunInput{Host: "api.openai.com"}, matcher)

	if !result.Determined || result.Action != "direct" || result.MatchedRule == nil || result.MatchedRule.ID != "fallback" {
		t.Fatalf("result = %#v, want determined fallback after rule-set miss", result)
	}
	if got := result.Trace[0].Status; got != "miss" {
		t.Fatalf("first status = %q, want miss", got)
	}
}

func TestEvaluateDefersToClientWhenRuleSetUnsupported(t *testing.T) {
	matcher := &mockRuleSetMatcher{results: map[string]RuleSetMatchResult{
		"claude": {Supported: false},
	}}
	result := Evaluate([]Rule{
		{ID: "rule-set", Name: "Rule set", Enabled: true, Action: "proxy", Conditions: Conditions{RuleSets: []string{"claude"}}},
		{ID: "fallback", Name: "Fallback", Enabled: true, Action: "direct"},
	}, "block", DryRunInput{Host: "claude.ai"}, matcher)

	if result.Determined {
		t.Fatalf("result = %#v, want undetermined for unsupported rule-set", result)
	}
	if got := result.Trace[0].Status; got != "client" {
		t.Fatalf("first status = %q, want client", got)
	}
}

func TestEvaluateDefersToClientWhenMatcherNil(t *testing.T) {
	result := Evaluate([]Rule{
		{ID: "rule-set", Name: "Rule set", Enabled: true, Action: "proxy", Conditions: Conditions{RuleSets: []string{"openai"}}},
	}, "block", DryRunInput{Host: "api.openai.com"}, nil)

	if result.Determined {
		t.Fatalf("result = %#v, want undetermined when matcher is nil", result)
	}
	if got := result.Trace[0].Status; got != "client" {
		t.Fatalf("first status = %q, want client", got)
	}
}
