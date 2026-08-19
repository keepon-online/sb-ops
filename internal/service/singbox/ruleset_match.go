package singbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
)

const (
	ruleSetFetchTimeout = 15 * time.Second
	ruleSetFetchLimit   = 32 << 20
	ruleSetCacheTTL     = 24 * time.Hour
)

// remoteRuleSetMatcher downloads sing-box .srs rule sets on demand and matches a
// host against them locally, so the dry-run can evaluate rule_set conditions
// without the live kernel. Results are cached in memory with a TTL.
type remoteRuleSetMatcher struct {
	client *http.Client

	mu    sync.RWMutex
	cache map[string]cachedRuleSet
}

type cachedRuleSet struct {
	rules   []option.HeadlessRule
	expires time.Time
}

func newRemoteRuleSetMatcher() *remoteRuleSetMatcher {
	return &remoteRuleSetMatcher{
		client: &http.Client{Timeout: ruleSetFetchTimeout},
		cache:  make(map[string]cachedRuleSet),
	}
}

// matchHost reports whether the rule set identified by tag contains host (a
// domain or an IP literal). Unknown tags or download failures return an error
// so the caller can fall back to "unknown".
func (m *remoteRuleSetMatcher) matchHost(ctx context.Context, url, host string) (bool, error) {
	rules, err := m.load(ctx, url)
	if err != nil {
		return false, err
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, rule := range rules {
		matched, supported, err := matchHeadlessRule(rule, host)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
		_ = supported // unsupported leaf conditions simply do not match
	}
	return false, nil
}

func (m *remoteRuleSetMatcher) load(ctx context.Context, url string) ([]option.HeadlessRule, error) {
	m.mu.RLock()
	if entry, ok := m.cache[url]; ok && time.Now().Before(entry.expires) {
		m.mu.RUnlock()
		return entry.rules, nil
	}
	m.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载规则集失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载规则集失败：HTTP %d", resp.StatusCode)
	}
	compat, err := srs.Read(io.LimitReader(resp.Body, ruleSetFetchLimit), true)
	if err != nil {
		return nil, fmt.Errorf("解析规则集失败: %w", err)
	}
	plain, err := compat.Upgrade()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[url] = cachedRuleSet{rules: plain.Rules, expires: time.Now().Add(ruleSetCacheTTL)}
	m.mu.Unlock()
	return plain.Rules, nil
}

// matchHeadlessRule evaluates one headless rule (default or logical) against a
// host. supported is false when the rule used conditions the local matcher
// cannot evaluate; callers treat those as non-matching.
func matchHeadlessRule(rule option.HeadlessRule, host string) (matched, supported bool, err error) {
	if rule.Type == "logical" {
		return matchLogicalRule(rule.LogicalOptions, host)
	}
	return matchDefaultRule(rule.DefaultOptions, host)
}

func matchLogicalRule(rule option.LogicalHeadlessRule, host string) (bool, bool, error) {
	supported := true
	for _, sub := range rule.Rules {
		matched, subSupported, err := matchHeadlessRule(sub, host)
		if err != nil {
			return false, false, err
		}
		if !subSupported {
			supported = false
			continue
		}
		switch strings.ToLower(strings.TrimSpace(rule.Mode)) {
		case "and":
			if !matched {
				return false, true, nil
			}
		default: // or
			if matched {
				return true, true, nil
			}
		}
	}
	if strings.ToLower(strings.TrimSpace(rule.Mode)) == "and" {
		return supported, supported, nil
	}
	return false, supported, nil
}

func matchDefaultRule(rule option.DefaultHeadlessRule, host string) (bool, bool, error) {
	if len(rule.Domain) == 0 && len(rule.DomainSuffix) == 0 && len(rule.DomainKeyword) == 0 &&
		len(rule.DomainRegex) == 0 && len(rule.IPCIDR) == 0 {
		// No locally matchable condition: unsupported (process/port-only rules etc.).
		return false, false, nil
	}
	for _, domain := range rule.Domain {
		if strings.EqualFold(domain, host) {
			return true, true, nil
		}
	}
	for _, suffix := range rule.DomainSuffix {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true, true, nil
		}
	}
	for _, keyword := range rule.DomainKeyword {
		if strings.Contains(host, strings.ToLower(keyword)) {
			return true, true, nil
		}
	}
	if address, err := netip.ParseAddr(host); err == nil {
		for _, cidr := range rule.IPCIDR {
			prefix, err := netip.ParsePrefix(cidr)
			if err != nil {
				return false, false, fmt.Errorf("规则集 CIDR %q 无效: %w", cidr, err)
			}
			if prefix.Contains(address) {
				return true, true, nil
			}
		}
	}
	return false, true, nil
}
