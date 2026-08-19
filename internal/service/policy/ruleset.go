package policy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ruleSetFormatMRS       = "mrs"
	ruleSetFormatClassical = "yaml" // classical YAML; not matched locally
	ruleSetCacheTTL        = 24 * time.Hour
	ruleSetDownloadTimeout = 10 * time.Second
	ruleSetDownloadLimit   = 50 << 20
)

// RuleSetSource describes a Clash rule-set provider. The URL points at the
// mihomo .mrs binary consumed by Clash rule-providers (the compiler emits it
// verbatim). The local matcher derives a sibling .list text URL from it.
type RuleSetSource struct {
	URL      string
	Behavior string // domain | ipcidr | classical
	Format   string // mrs | yaml
}

// ClashRuleSetSources returns the canonical name→source mapping shared by the
// Clash subscription compiler and the policy dry-run matcher. Keeping it in the
// policy package makes it the single source of truth; the compiler already
// imports this package, so there is no import cycle.
func ClashRuleSetSources() map[string]RuleSetSource {
	const metaBaseURL = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo"
	const legacyBaseURL = "https://raw.githubusercontent.com/blackmatrix7/ios_rule_script/master/rule/Clash"
	sources := map[string]RuleSetSource{}
	for _, name := range []string{"category-ads-all", "category-ai-!cn", "openai", "bilibili", "biliintl", "youtube", "google", "geolocation-cn", "telegram", "github", "gitlab", "microsoft", "apple", "applemusic", "facebook", "instagram", "twitter", "tiktok", "linkedin", "netflix", "hulu", "disney", "hbo", "amazon", "bahamut", "coursera", "edx", "udemy", "khanacademy", "category-scholar-!cn", "proxy"} {
		sources[name] = RuleSetSource{Behavior: "domain", Format: ruleSetFormatMRS, URL: metaBaseURL + "/geosite/" + name + ".mrs"}
	}
	for _, name := range []string{"google-ip", "private", "cn", "telegram-ip"} {
		fileName := name
		if name == "telegram-ip" {
			fileName = "telegram"
		}
		sources[name] = RuleSetSource{Behavior: "ipcidr", Format: ruleSetFormatMRS, URL: metaBaseURL + "/geoip/" + fileName + ".mrs"}
	}
	for name, path := range map[string]string{
		"claude":  "/Claude/Claude.yaml",
		"china":   "/ChinaMax/ChinaMax.yaml",
		"spotify": "/Spotify/Spotify.yaml",
		"steam":   "/Steam/Steam.yaml",
	} {
		sources[name] = RuleSetSource{Behavior: "classical", Format: ruleSetFormatClassical, URL: legacyBaseURL + path}
	}
	return sources
}

// RuleSetMatchResult describes the outcome of evaluating one rule-set against a host.
type RuleSetMatchResult struct {
	Matched     bool
	Supported   bool  // false for unknown names or non-mrs formats
	DownloadErr error // non-nil when the provider could not be fetched or parsed
}

// RuleSetMatcher resolves whether a host belongs to a named Clash rule-set, so
// the dry-run can determine RULE-SET conditions instead of deferring to the client.
type RuleSetMatcher interface {
	Match(ctx context.Context, name, host string) RuleSetMatchResult
}

// compiledRuleSet is a parsed Clash .list rule-set ready for local matching.
// Domains and IP CIDRs are kept separately so a domain input never matches an
// ipcidr set (and vice versa), mirroring Clash semantics.
type compiledRuleSet struct {
	domains  map[string]bool
	suffixes []string
	cidrs    []netip.Prefix
	expires  time.Time
}

func (c compiledRuleSet) match(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if addr, err := netip.ParseAddr(host); err == nil {
		for _, prefix := range c.cidrs {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	if c.domains[host] {
		return true
	}
	for _, suffix := range c.suffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

type remoteRuleSetMatcher struct {
	client *http.Client
	ttl    time.Duration

	mu    sync.RWMutex
	cache map[string]compiledRuleSet
}

// NewRuleSetMatcher builds the default RuleSetMatcher that downloads and caches
// the text form (.list) of rule-set providers on demand. Only MetaCubeX .mrs
// providers have a .list sibling; classical YAML providers report unsupported.
func NewRuleSetMatcher() RuleSetMatcher {
	return &remoteRuleSetMatcher{
		client: &http.Client{Timeout: ruleSetDownloadTimeout},
		ttl:    ruleSetCacheTTL,
		cache:  make(map[string]compiledRuleSet),
	}
}

func (m *remoteRuleSetMatcher) Match(ctx context.Context, name, host string) RuleSetMatchResult {
	source, ok := ClashRuleSetSources()[name]
	if !ok {
		// Unknown rule-set name (e.g. a renamed/retired provider still referenced
		// by stored rules): treat as an empty set that matches nothing, so it does
		// not force the whole rule to defer to the client.
		return RuleSetMatchResult{Supported: true}
	}
	// Both MetaCubeX .mrs and blackmatrix7 .yaml providers publish a sibling
	// .list text file we can parse locally; nothing is deferred to the client.
	compiled, err := m.load(ctx, name, source)
	if err != nil {
		return RuleSetMatchResult{Supported: true, DownloadErr: err}
	}
	return RuleSetMatchResult{Matched: compiled.match(host), Supported: true}
}

// RuleSetOption is one entry in the Clash rule-set catalog exposed to the UI so
// a subscription rule can pick a rule-set by name instead of typing it.
type RuleSetOption struct {
	Name     string `json:"name"`
	Behavior string `json:"behavior"` // domain | ipcidr | classical
}

// RuleSetOptions returns the sorted catalog of known Clash rule-set names,
// derived from the same source mapping used by the subscription compiler.
func RuleSetOptions() []RuleSetOption {
	sources := ClashRuleSetSources()
	options := make([]RuleSetOption, 0, len(sources))
	for name, source := range sources {
		options = append(options, RuleSetOption{Name: name, Behavior: source.Behavior})
	}
	slices.SortFunc(options, func(a, b RuleSetOption) int {
		return strings.Compare(a.Name, b.Name)
	})
	return options
}

func (m *remoteRuleSetMatcher) load(ctx context.Context, name string, source RuleSetSource) (*compiledRuleSet, error) {
	m.mu.RLock()
	if entry, ok := m.cache[name]; ok && time.Now().Before(entry.expires) {
		m.mu.RUnlock()
		return &entry, nil
	}
	m.mu.RUnlock()

	compiled, err := fetchAndParseList(ctx, m.client, source)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.cache[name] = *compiled
	m.mu.Unlock()
	return compiled, nil
}

// listURLFor derives the text .list URL from a provider's .mrs URL. MetaCubeX
// publishes both formats side-by-side under the same path.
func listURLFor(source RuleSetSource) (string, bool) {
	for _, ext := range []string{".mrs", ".yaml"} {
		if strings.HasSuffix(source.URL, ext) {
			return strings.TrimSuffix(source.URL, ext) + ".list", true
		}
	}
	return "", false
}

func fetchAndParseList(ctx context.Context, client *http.Client, source RuleSetSource) (*compiledRuleSet, error) {
	listURL, ok := listURLFor(source)
	if !ok {
		return nil, fmt.Errorf("规则集 %s 没有可本地解析的文本版本", source.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Provider has no text sibling at this path; treat as an empty set.
		return &compiledRuleSet{domains: map[string]bool{}, expires: time.Now().Add(ruleSetCacheTTL)}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败：HTTP %d", listURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, ruleSetDownloadLimit))
	if err != nil {
		return nil, err
	}
	return parseList(data, time.Now().Add(ruleSetCacheTTL)), nil
}

// parseList parses a Clash .list payload. Each non-comment line is one of:
//   - "+.suffix" / "*.suffix" → domain suffix match (suffix and *.suffix)
//   - "1.2.3.0/24" / "1.2.3.4" → IP CIDR / single IP
//   - anything else → exact domain match
func parseList(data []byte, expires time.Time) *compiledRuleSet {
	compiled := &compiledRuleSet{domains: map[string]bool{}, expires: expires}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Classical .list lines look like "TYPE,value[,policy]"; geosite .list
		// lines are bare domains/CIDRs. Distinguish by the leading token.
		if comma := strings.IndexByte(line, ','); comma >= 0 {
			typ := strings.ToUpper(strings.TrimSpace(line[:comma]))
			val := strings.TrimSpace(line[comma+1:])
			if extra := strings.IndexByte(val, ','); extra >= 0 {
				val = strings.TrimSpace(val[:extra])
			}
			switch typ {
			case "DOMAIN":
				compiled.domains[strings.ToLower(val)] = true
				continue
			case "DOMAIN-SUFFIX":
				compiled.suffixes = append(compiled.suffixes, strings.ToLower(val))
				continue
			case "IP-CIDR", "IP-CIDR6":
				if prefix, err := netip.ParsePrefix(val); err == nil {
					compiled.cidrs = append(compiled.cidrs, prefix)
				}
				continue
			case "DOMAIN-KEYWORD", "DOMAIN-REGEX", "GEOIP", "GEOLOCATION", "PROCESS-NAME", "PROCESS-PATH", "RULE-SET", "DST-PORT", "SRC-PORT":
				// Recognized classical types that are not locally matchable: skip.
				continue
			default:
				// Not a classical type; fall through to geosite parsing.
			}
		}
		if prefix, err := netip.ParsePrefix(line); err == nil {
			compiled.cidrs = append(compiled.cidrs, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(line); err == nil {
			compiled.cidrs = append(compiled.cidrs, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "+.") {
			compiled.suffixes = append(compiled.suffixes, lower[2:])
		} else if strings.HasPrefix(lower, "*.") {
			compiled.suffixes = append(compiled.suffixes, lower[2:])
		} else {
			compiled.domains[lower] = true
		}
	}
	return compiled
}
