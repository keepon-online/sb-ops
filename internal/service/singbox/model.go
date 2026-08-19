package singbox

import "time"

// Outbound type constants. SOCKS rows use the legacy server/port/username
// columns; WARP rows store a wireguard endpoint description in the encrypted
// settings blob.
const (
	OutboundTypeSOCKS = "socks"
	OutboundTypeWarp  = "warp"
	// DefaultWarpEndpoint is the Cloudflare WARP registration endpoint used
	// when a WARP outbound does not specify one.
	DefaultWarpEndpoint = "engage.cloudflareclient.com:2408"
)

type DNSServer struct {
	Tag     string `json:"tag"`
	Address string `json:"address"`
	Detour  string `json:"detour,omitempty"`
}

type Config struct {
	LogLevel           string      `json:"logLevel"`
	LogTimestamp       bool        `json:"logTimestamp"`
	DNSServers         []DNSServer `json:"dnsServers"`
	DNSFinal           string      `json:"dnsFinal,omitempty"`
	ClashAPIEnabled    bool        `json:"clashApiEnabled"`
	ClashAPIListen     string      `json:"clashApiListen"`
	ClashAPISecretSet  bool        `json:"clashApiSecretSet"`
	V2RayAPIEnabled    bool        `json:"v2rayApiEnabled"`
	V2RayAPIListen     string      `json:"v2rayApiListen"`
	DefaultOutboundTag string      `json:"defaultOutboundTag"`
	Revision           int64       `json:"revision"`
	UpdatedAt          time.Time   `json:"updatedAt"`
}

// WarpSettings carries the wireguard parameters of a WARP outbound. The
// private key is write-only through the API: reads replace it with
// PrivateKeySet and an update may omit it to keep the stored value.
type WarpSettings struct {
	Endpoint      string `json:"endpoint"`
	PublicKey     string `json:"publicKey"`
	PrivateKey    string `json:"privateKey,omitempty"`
	PrivateKeySet bool   `json:"privateKeySet,omitempty"`
	IPv4          string `json:"ipv4"`
	IPv6          string `json:"ipv6,omitempty"`
	Reserved      []int  `json:"reserved,omitempty"`
	MTU           int    `json:"mtu,omitempty"`
}

type Outbound struct {
	ID          string        `json:"id"`
	Tag         string        `json:"tag"`
	Type        string        `json:"type"`
	Server      string        `json:"server,omitempty"`
	ServerPort  int           `json:"serverPort,omitempty"`
	Username    string        `json:"username,omitempty"`
	HasPassword bool          `json:"hasPassword"`
	Warp        *WarpSettings `json:"warp,omitempty"`
	Revision    int64         `json:"revision"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
}

type RouteConditions struct {
	Domains        []string `json:"domains,omitempty"`
	DomainSuffixes []string `json:"domainSuffixes,omitempty"`
	IPCIDRs        []string `json:"ipCidrs,omitempty"`
	Ports          []int    `json:"ports,omitempty"`
	Networks       []string `json:"networks,omitempty"`
	InboundTags    []string `json:"inboundTags,omitempty"`
	GeoIP          []string `json:"geoip,omitempty"`
	GeoSite        []string `json:"geosite,omitempty"`
	RuleSets       []string `json:"ruleSets,omitempty"`
}

type RouteRule struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Enabled     bool            `json:"enabled"`
	Priority    int             `json:"priority"`
	Conditions  RouteConditions `json:"conditions"`
	OutboundTag string          `json:"outboundTag"`
	Revision    int64           `json:"revision"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type ConfigWriteInput struct {
	Revision           int64
	LogLevel           string
	LogTimestamp       bool
	DNSServers         []DNSServer
	DNSFinal           string
	ClashAPIEnabled    bool
	ClashAPIListen     string
	ClashAPISecret     *string
	V2RayAPIEnabled    bool
	V2RayAPIListen     string
	DefaultOutboundTag string
}

type OutboundWriteInput struct {
	ID         string
	Revision   int64
	Type       string
	Tag        string
	Server     string
	ServerPort int
	Username   string
	Password   *string
	Warp       *WarpSettings
}

type RouteRuleWriteInput struct {
	ID          string
	Revision    int64
	Name        string
	Enabled     bool
	Priority    int
	Conditions  RouteConditions
	OutboundTag string
}

// RuleSet is a named, remotely downloaded rule set referenced by server-side
// route rules via RouteConditions.RuleSets. Only remote (URL) rule sets are
// managed by the panel; sing-box downloads and caches them through cache_file.
type RuleSet struct {
	ID             string    `json:"id"`
	Tag            string    `json:"tag"`
	Format         string    `json:"format"`
	URL            string    `json:"url"`
	DownloadDetour string    `json:"downloadDetour,omitempty"`
	Revision       int64     `json:"revision"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type RuleSetWriteInput struct {
	ID             string
	Revision       int64
	Tag            string
	Format         string
	URL            string
	DownloadDetour string
}

type OrderInput struct {
	IDs []string `json:"ids"`
}

type DryRunInput struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Network    string `json:"network"`
	InboundTag string `json:"inboundTag"`
}

type Evaluation struct {
	RuleID      string `json:"ruleId"`
	Name        string `json:"name"`
	Matched     bool   `json:"matched"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	OutboundTag string `json:"outboundTag,omitempty"`
}

type DryRunResult struct {
	Trace       []Evaluation `json:"trace"`
	MatchedRule *RouteRule   `json:"matchedRule,omitempty"`
	OutboundTag string       `json:"outboundTag"`
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
