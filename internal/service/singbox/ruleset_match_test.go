package singbox

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestMatchDefaultRuleDomainConditions(t *testing.T) {
	rule := option.DefaultHeadlessRule{
		Domain:       []string{"ai.google.dev"},
		DomainSuffix: []string{"gemini.google.com", "generativelanguage.googleapis.com"},
	}
	if matched, supported, err := matchDefaultRule(rule, "gemini.google.com"); err != nil || !matched || !supported {
		t.Fatalf("suffix match = %v/%v err=%v", matched, supported, err)
	}
	if matched, _, _ := matchDefaultRule(rule, "a.gemini.google.com"); !matched {
		t.Fatal("nested suffix should match")
	}
	if matched, _, _ := matchDefaultRule(rule, "google.com"); matched {
		t.Fatal("bare suffix parent should not match")
	}
	if matched, _, _ := matchDefaultRule(rule, "ai.google.dev"); !matched {
		t.Fatal("exact domain should match")
	}
}

func TestMatchDefaultRuleIPConditions(t *testing.T) {
	rule := option.DefaultHeadlessRule{IPCIDR: []string{"142.250.0.0/15"}}
	if matched, _, err := matchDefaultRule(rule, "142.251.36.14"); err != nil || !matched {
		t.Fatalf("cidr match = %v err=%v", matched, err)
	}
	if matched, _, _ := matchDefaultRule(rule, "8.8.8.8"); matched {
		t.Fatal("outside cidr should not match")
	}
}

func TestMatchLogicalRule(t *testing.T) {
	rule := option.HeadlessRule{
		Type: "logical",
		LogicalOptions: option.LogicalHeadlessRule{
			Mode: "or",
			Rules: []option.HeadlessRule{
				{DefaultOptions: option.DefaultHeadlessRule{DomainSuffix: []string{"openai.com"}}},
				{DefaultOptions: option.DefaultHeadlessRule{DomainSuffix: []string{"anthropic.com"}}},
			},
		},
	}
	if matched, supported, err := matchHeadlessRule(rule, "chat.openai.com"); err != nil || !matched || !supported {
		t.Fatalf("logical or match = %v/%v err=%v", matched, supported, err)
	}
	if matched, _, _ := matchHeadlessRule(rule, "example.com"); matched {
		t.Fatal("logical or should miss unrelated host")
	}
}

// TestRemoteRuleSetMatcherLive downloads the official SagerNet openai rule set
// and verifies a known member matches. Skipped when the network is unavailable.
func TestRemoteRuleSetMatcherLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test skipped in short mode")
	}
	matcher := newRemoteRuleSetMatcher()
	matched, err := matcher.matchHost(context.Background(),
		"https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-openai.srs", "chat.openai.com")
	if err != nil {
		t.Skipf("network unavailable: %v", err)
	}
	if !matched {
		t.Fatal("chat.openai.com should be inside the openai rule set")
	}
}
