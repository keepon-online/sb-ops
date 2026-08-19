package policy

import "time"

type Policy struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Enabled       bool      `json:"enabled"`
	DefaultAction string    `json:"defaultAction"`
	Revision      int64     `json:"revision"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Conditions struct {
	Domains        []string `json:"domains,omitempty"`
	DomainSuffixes []string `json:"domainSuffixes,omitempty"`
	IPCIDRs        []string `json:"ipCidrs,omitempty"`
	RuleSets       []string `json:"ruleSets,omitempty"`
	Ports          []int    `json:"ports,omitempty"`
	Networks       []string `json:"networks,omitempty"`
	Protocols      []string `json:"protocols,omitempty"`
}

type Rule struct {
	ID         string     `json:"id"`
	PolicyID   string     `json:"policyId"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Priority   int        `json:"priority"`
	Conditions Conditions `json:"conditions"`
	Action     string     `json:"action"`
	NoResolve  bool       `json:"noResolve"`
	Revision   int64      `json:"revision"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type PolicyWriteInput struct {
	ID            string
	Revision      int64
	Name          string
	Description   string
	Enabled       bool
	DefaultAction string
}

type RuleWriteInput struct {
	ID         string
	PolicyID   string
	Revision   int64
	Name       string
	Enabled    bool
	Priority   int
	Conditions Conditions
	Action     string
	NoResolve  bool
}

type CloneInput struct {
	Name string `json:"name"`
}

type OrderInput struct {
	IDs []string `json:"ids"`
}

type DryRunInput struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Protocol string `json:"protocol"`
}

type Evaluation struct {
	RuleID  string `json:"ruleId"`
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Action  string `json:"action,omitempty"`
}

type DryRunResult struct {
	Trace       []Evaluation `json:"trace"`
	MatchedRule *Rule        `json:"matchedRule,omitempty"`
	Action      string       `json:"action"`
	Determined  bool         `json:"determined"`
}

type ValidationIssue struct {
	Level   string `json:"level"`
	RuleID  string `json:"ruleId,omitempty"`
	Message string `json:"message"`
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues"`
}
