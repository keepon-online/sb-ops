package policy

import (
	"context"
	"strings"
)

const DefaultClashPolicyName = "Clash 默认分流"

func (s *Service) EnsureDefaultClashPolicy(ctx context.Context) (Policy, error) {
	policies, err := s.List(ctx)
	if err != nil {
		return Policy{}, err
	}
	for _, value := range policies {
		if strings.EqualFold(value.Name, DefaultClashPolicyName) {
			if err := s.ensureDefaultClashRules(ctx, value); err != nil {
				return Policy{}, err
			}
			return value, s.store.AssignPolicyToUsersWithoutPolicies(ctx, value.ID)
		}
	}
	value, err := s.Create(ctx, PolicyWriteInput{
		Name: DefaultClashPolicyName, Description: "内置常用 Clash 分流：私网与国内直连，常用海外服务代理", Enabled: true, DefaultAction: "proxy",
	})
	if err != nil {
		return Policy{}, err
	}
	if err := s.ensureDefaultClashRules(ctx, value); err != nil {
		return Policy{}, err
	}
	return value, s.store.AssignPolicyToUsersWithoutPolicies(ctx, value.ID)
}

func (s *Service) ensureDefaultClashRules(ctx context.Context, policy Policy) error {
	existing, err := s.store.ListRules(ctx, policy.ID)
	if err != nil {
		return err
	}
	byName := make(map[string]bool, len(existing))
	usedPriorities := make(map[int]bool, len(existing))
	for _, rule := range existing {
		byName[rule.Name] = true
		usedPriorities[rule.Priority] = true
	}
	for _, rule := range defaultClashRules() {
		if byName[rule.Name] {
			continue
		}
		priority := availableDefaultRulePriority(rule.Priority, usedPriorities)
		if _, err := s.CreateRule(ctx, RuleWriteInput{
			PolicyID: policy.ID, Name: rule.Name, Enabled: true, Priority: priority,
			Conditions: rule.Conditions, Action: rule.Action, NoResolve: rule.NoResolve,
		}); err != nil {
			return err
		}
		usedPriorities[priority] = true
	}
	return nil
}

func availableDefaultRulePriority(preferred int, used map[int]bool) int {
	for priority := preferred; priority >= 0; priority-- {
		if !used[priority] {
			return priority
		}
	}
	for priority := preferred + 1; ; priority++ {
		if !used[priority] {
			return priority
		}
	}
}

func defaultClashRules() []RuleWriteInput {
	return []RuleWriteInput{
		{Name: "广告拦截", Priority: 200, Action: "block", Conditions: Conditions{RuleSets: []string{"category-ads-all"}}},
		{Name: "私网与局域网", Priority: 190, Action: "direct", NoResolve: true, Conditions: Conditions{Domains: []string{"local"}, IPCIDRs: []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "::1/128", "fc00::/7", "fe80::/10"}}},
		{Name: "私有网络", Priority: 185, Action: "direct", NoResolve: true, Conditions: Conditions{RuleSets: []string{"private"}}},
		{Name: "AI 服务", Priority: 180, Action: "proxy", Conditions: Conditions{RuleSets: []string{"category-ai-!cn", "openai", "claude"}}},
		{Name: "哔哩哔哩", Priority: 170, Action: "proxy", Conditions: Conditions{RuleSets: []string{"bilibili", "biliintl"}}},
		{Name: "油管视频", Priority: 160, Action: "proxy", Conditions: Conditions{RuleSets: []string{"youtube"}}},
		{Name: "谷歌服务", Priority: 150, Action: "proxy", NoResolve: true, Conditions: Conditions{RuleSets: []string{"google", "google-ip"}}},
		{Name: "国内服务", Priority: 140, Action: "direct", NoResolve: true, Conditions: Conditions{RuleSets: []string{"geolocation-cn", "cn"}}},
		{Name: "电报消息", Priority: 130, Action: "proxy", NoResolve: true, Conditions: Conditions{RuleSets: []string{"telegram", "telegram-ip"}}},
		{Name: "GitHub", Priority: 120, Action: "proxy", Conditions: Conditions{RuleSets: []string{"github", "gitlab"}}},
		{Name: "微软服务", Priority: 110, Action: "proxy", Conditions: Conditions{RuleSets: []string{"microsoft"}}},
		{Name: "苹果服务", Priority: 100, Action: "proxy", Conditions: Conditions{RuleSets: []string{"apple", "applemusic"}}},
		{Name: "社交媒体", Priority: 90, Action: "proxy", Conditions: Conditions{RuleSets: []string{"facebook", "instagram", "twitter", "tiktok", "linkedin"}}},
		{Name: "流媒体", Priority: 80, Action: "proxy", Conditions: Conditions{RuleSets: []string{"netflix", "hulu", "disney", "hbo", "amazon", "bahamut", "spotify"}}},
		{Name: "教育资源", Priority: 70, Action: "proxy", Conditions: Conditions{RuleSets: []string{"coursera", "edx", "udemy", "khanacademy", "category-scholar-!cn"}}},
		{Name: "代理服务", Priority: 60, Action: "proxy", Conditions: Conditions{RuleSets: []string{"proxy", "steam"}}},
	}
}
