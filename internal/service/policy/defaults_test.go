package policy

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/keepon-online/sb-ops/internal/database"
	_ "github.com/mattn/go-sqlite3"
)

func TestRuleSetOptionsAreSortedAndCoverDefaults(t *testing.T) {
	options := RuleSetOptions()
	if len(options) == 0 {
		t.Fatal("rule set catalog is empty")
	}
	names := make([]string, 0, len(options))
	for _, option := range options {
		names = append(names, option.Name)
	}
	if !slices.IsSorted(names) {
		t.Fatalf("rule set catalog is not sorted: %v", names)
	}
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	// Every rule-set name referenced by the built-in default rules must be
	// selectable in the catalog.
	for _, rule := range defaultClashRules() {
		for _, name := range rule.Conditions.RuleSets {
			if !known[name] {
				t.Fatalf("default rule set %q missing from catalog", name)
			}
		}
	}
}

func TestEnsureDefaultClashPolicyAddsRulesToLegacyPolicyWithOccupiedPriorities(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	service := NewService(NewStore(db))
	policy, err := service.Create(t.Context(), PolicyWriteInput{
		Name: DefaultClashPolicyName, Enabled: true, DefaultAction: "proxy",
	})
	if err != nil {
		t.Fatalf("create legacy policy: %v", err)
	}
	for _, priority := range []int{100, 90, 80, 70, 60} {
		if _, err := service.CreateRule(t.Context(), RuleWriteInput{
			PolicyID: policy.ID, Name: "legacy rule", Enabled: true, Priority: priority,
			Action: "direct", Conditions: Conditions{Domains: []string{"legacy.example"}},
		}); err != nil {
			t.Fatalf("create legacy priority %d: %v", priority, err)
		}
	}

	if _, err := service.EnsureDefaultClashPolicy(t.Context()); err != nil {
		t.Fatalf("upgrade legacy default policy: %v", err)
	}
	rules, err := service.ListRules(t.Context(), policy.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != len(defaultClashRules())+5 {
		t.Fatalf("rule count = %d, want %d", len(rules), len(defaultClashRules())+5)
	}
}
