package policy

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Service struct {
	store   *Store
	now     func() time.Time
	matcher RuleSetMatcher
}

func NewService(store *Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }, matcher: NewRuleSetMatcher()}
}

func (s *Service) Create(ctx context.Context, input PolicyWriteInput) (Policy, error) {
	value, fields := policyFromInput(input)
	if len(fields) > 0 {
		return Policy{}, &common.ValidationError{Fields: fields}
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreatePolicy(ctx, value); err != nil {
		return Policy{}, err
	}
	return value, nil
}
func (s *Service) List(ctx context.Context) ([]Policy, error) { return s.store.ListPolicies(ctx) }
func (s *Service) Get(ctx context.Context, id string) (Policy, error) {
	return s.store.GetPolicy(ctx, id)
}
func (s *Service) Update(ctx context.Context, input PolicyWriteInput) (Policy, error) {
	if input.Revision < 1 {
		return Policy{}, &common.ValidationError{Fields: map[string]string{"revision": "must be positive"}}
	}
	value, fields := policyFromInput(input)
	if len(fields) > 0 {
		return Policy{}, &common.ValidationError{Fields: fields}
	}
	current, err := s.store.GetPolicy(ctx, input.ID)
	if err != nil {
		return Policy{}, err
	}
	value.ID, value.CreatedAt, value.UpdatedAt = current.ID, current.CreatedAt, s.now()
	if err := s.store.UpdatePolicy(ctx, value, input.Revision); err != nil {
		return Policy{}, err
	}
	return s.Get(ctx, input.ID)
}
func (s *Service) Delete(ctx context.Context, id string) error { return s.store.DeletePolicy(ctx, id) }
func (s *Service) Clone(ctx context.Context, id string, input CloneInput) (Policy, error) {
	current, err := s.store.GetPolicy(ctx, id)
	if err != nil {
		return Policy{}, err
	}
	created, err := s.Create(ctx, PolicyWriteInput{Name: input.Name, Description: current.Description, Enabled: false, DefaultAction: current.DefaultAction})
	if err != nil {
		return Policy{}, err
	}
	rules, err := s.store.ListRules(ctx, id)
	if err != nil {
		return Policy{}, err
	}
	for _, rule := range rules {
		_, err = s.CreateRule(ctx, RuleWriteInput{PolicyID: created.ID, Name: rule.Name, Enabled: rule.Enabled, Priority: rule.Priority, Conditions: rule.Conditions, Action: rule.Action, NoResolve: rule.NoResolve})
		if err != nil {
			return Policy{}, err
		}
	}
	return created, nil
}
func (s *Service) ListRules(ctx context.Context, policyID string) ([]Rule, error) {
	if _, err := s.store.GetPolicy(ctx, policyID); err != nil {
		return nil, err
	}
	return s.store.ListRules(ctx, policyID)
}

// GetRule 返回策略内的单条规则，更新 handler 用它取改前快照计算审计 diff。
func (s *Service) GetRule(ctx context.Context, policyID, id string) (Rule, error) {
	if _, err := s.store.GetPolicy(ctx, policyID); err != nil {
		return Rule{}, err
	}
	return s.store.GetRule(ctx, policyID, id)
}
func (s *Service) CreateRule(ctx context.Context, input RuleWriteInput) (Rule, error) {
	value, fields := ruleFromInput(input)
	if len(fields) > 0 {
		return Rule{}, &common.ValidationError{Fields: fields}
	}
	if _, err := s.store.GetPolicy(ctx, input.PolicyID); err != nil {
		return Rule{}, err
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.CreateRule(ctx, value); err != nil {
		return Rule{}, err
	}
	return value, nil
}
func (s *Service) UpdateRule(ctx context.Context, input RuleWriteInput) (Rule, error) {
	if input.Revision < 1 {
		return Rule{}, &common.ValidationError{Fields: map[string]string{"revision": "must be positive"}}
	}
	value, fields := ruleFromInput(input)
	if len(fields) > 0 {
		return Rule{}, &common.ValidationError{Fields: fields}
	}
	current, err := s.store.GetRule(ctx, input.PolicyID, input.ID)
	if err != nil {
		return Rule{}, err
	}
	value.ID, value.PolicyID, value.CreatedAt, value.UpdatedAt = current.ID, current.PolicyID, current.CreatedAt, s.now()
	if err := s.store.UpdateRule(ctx, value, input.Revision); err != nil {
		return Rule{}, err
	}
	return s.store.GetRule(ctx, input.PolicyID, input.ID)
}
func (s *Service) DeleteRule(ctx context.Context, policyID, id string) error {
	return s.store.DeleteRule(ctx, policyID, id)
}
func (s *Service) OrderRules(ctx context.Context, policyID string, input OrderInput) ([]Rule, error) {
	if err := s.store.OrderRules(ctx, policyID, input.IDs, s.now()); err != nil {
		return nil, err
	}
	return s.store.ListRules(ctx, policyID)
}
func (s *Service) DryRun(ctx context.Context, policyID string, input DryRunInput) (DryRunResult, error) {
	policy, err := s.store.GetPolicy(ctx, policyID)
	if err != nil {
		return DryRunResult{}, err
	}
	if strings.TrimSpace(input.Host) == "" {
		return DryRunResult{}, &common.ValidationError{Fields: map[string]string{"host": "is required"}}
	}
	rules, err := s.store.ListRules(ctx, policyID)
	if err != nil {
		return DryRunResult{}, err
	}
	return Evaluate(rules, policy.DefaultAction, input, s.matcher), nil
}
func (s *Service) Validate(ctx context.Context, policyID string) (ValidationResult, error) {
	rules, err := s.store.ListRules(ctx, policyID)
	if err != nil {
		return ValidationResult{}, err
	}
	result := ValidationResult{Valid: true, Issues: []ValidationIssue{}}
	catchAll := false
	signatures := map[string]Rule{}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		if catchAll {
			result.Issues = append(result.Issues, ValidationIssue{Level: "warning", RuleID: rule.ID, Message: "rule is unreachable after a catch-all rule"})
		}
		raw, _ := json.Marshal(rule.Conditions)
		signature := string(raw)
		if previous, ok := signatures[signature]; ok && previous.Action != rule.Action {
			result.Issues = append(result.Issues, ValidationIssue{Level: "warning", RuleID: rule.ID, Message: "conditions conflict with rule " + previous.Name})
		} else {
			signatures[signature] = rule
		}
		if emptyConditions(rule.Conditions) {
			catchAll = true
		}
	}
	return result, nil
}

func policyFromInput(input PolicyWriteInput) (Policy, map[string]string) {
	fields := map[string]string{}
	name, action := strings.TrimSpace(input.Name), strings.TrimSpace(input.DefaultAction)
	if name == "" {
		fields["name"] = "is required"
	}
	if action == "" {
		fields["defaultAction"] = "is required"
	} else if !validAction(action) {
		// 动作非法入库会让之后每一次订阅编译失败（clashAction 报错），
		// 全站同步 409——在写入期以 fieldErrors 拒绝。
		fields["defaultAction"] = "must be one of direct, proxy, block"
	}
	return Policy{Name: name, Description: strings.TrimSpace(input.Description), Enabled: input.Enabled, DefaultAction: action}, fields
}
func ruleFromInput(input RuleWriteInput) (Rule, map[string]string) {
	fields := map[string]string{}
	name, action := strings.TrimSpace(input.Name), strings.TrimSpace(input.Action)
	if name == "" {
		fields["name"] = "is required"
	}
	if action == "" {
		fields["action"] = "is required"
	} else if !validAction(action) {
		fields["action"] = "must be one of direct, proxy, block"
	}
	if input.Priority < 0 {
		fields["priority"] = "must not be negative"
	}
	conditions := normalizeConditions(input.Conditions)
	for _, value := range conditions.IPCIDRs {
		if _, err := netip.ParsePrefix(value); err != nil {
			fields["conditions.ipCidrs"] = "contains an invalid CIDR"
			break
		}
	}
	for _, port := range conditions.Ports {
		if port < 1 || port > 65535 {
			fields["conditions.ports"] = "must contain ports between 1 and 65535"
			break
		}
	}
	for _, value := range conditions.Networks {
		if value != "tcp" && value != "udp" {
			fields["conditions.networks"] = "must contain only tcp or udp"
			break
		}
	}
	// 条件类型之间是 AND 语义（评估器如此判定），但 clash.yaml 经典规则
	// 无法跨类型组合：端口/网络条件只接受单条件规则，应用层协议条件无对应
	// 规则类型——写入期拒绝，勿让 dry-run 与实际产物静默分叉。
	if len(conditions.Protocols) > 0 {
		fields["conditions.protocols"] = "is not expressible in clash subscriptions"
	}
	groups := 0
	for _, present := range []bool{
		len(conditions.Domains) > 0 || len(conditions.DomainSuffixes) > 0 || len(conditions.IPCIDRs) > 0 || len(conditions.RuleSets) > 0,
		len(conditions.Ports) > 0,
		len(conditions.Networks) > 0,
	} {
		if present {
			groups++
		}
	}
	if groups > 1 {
		fields["conditions"] = "port and network conditions cannot be combined with other condition types"
	}
	return Rule{PolicyID: input.PolicyID, Name: name, Enabled: input.Enabled, Priority: input.Priority, Conditions: conditions, Action: action, NoResolve: input.NoResolve}, fields
}

// validAction 是订阅分流动作的封闭枚举，编译端 clashAction 必须同步认可。
func validAction(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "direct", "proxy", "block":
		return true
	default:
		return false
	}
}
func normalizeConditions(value Conditions) Conditions {
	value.Domains = normalizeStrings(value.Domains)
	value.DomainSuffixes = normalizeStrings(value.DomainSuffixes)
	value.IPCIDRs = normalizeStrings(value.IPCIDRs)
	value.RuleSets = normalizeStrings(value.RuleSets)
	value.Networks = normalizeStrings(value.Networks)
	value.Protocols = normalizeStrings(value.Protocols)
	slices.Sort(value.Ports)
	value.Ports = slices.Compact(value.Ports)
	return value
}
func normalizeStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}
func emptyConditions(value Conditions) bool {
	return len(value.Domains)+len(value.DomainSuffixes)+len(value.IPCIDRs)+len(value.RuleSets)+len(value.Ports)+len(value.Networks)+len(value.Protocols) == 0
}
