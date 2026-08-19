package protocol

import (
	"strings"
	"time"
)

type Type string

const (
	TypeVLESS       Type = "vless"
	TypeVMess       Type = "vmess"
	TypeHysteria2   Type = "hysteria2"
	TypeTUIC        Type = "tuic"
	TypeAnyTLS      Type = "anytls"
	TypeShadowsocks Type = "shadowsocks"
	TypeTrojan      Type = "trojan"
)

var Types = []Type{TypeVLESS, TypeVMess, TypeHysteria2, TypeTUIC, TypeAnyTLS, TypeShadowsocks, TypeTrojan}

type Protocol struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Type          Type           `json:"type"`
	Enabled       bool           `json:"enabled"`
	Listen        string         `json:"listen"`
	ListenPort    int            `json:"listenPort"`
	Settings      map[string]any `json:"settings"`
	CertificateID *string        `json:"certificateId,omitempty"`
	Revision      int64          `json:"revision"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

// InboundTag derives the unique, stable inbound tag used in the compiled
// sing-box config and the V2Ray stats API. It is "<type>-<slug(name)>"; protocol
// names are unique (enforced by the DB), so tags cannot collide across
// protocols of the same type.
func (p Protocol) InboundTag() string {
	slug := slugify(p.Name)
	if slug == "" {
		slug = p.ID
	}
	return string(p.Type) + "-" + slug
}

func slugify(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-':
			builder.WriteRune(r)
		case r == ' ' || r == '_' || r == '.':
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

type WriteInput struct {
	ID            string
	Revision      int64
	Name          string
	Type          Type
	Enabled       bool
	Listen        string
	ListenPort    int
	Settings      map[string]any
	CertificateID *string
}

type CloneInput struct {
	Name       string `json:"name"`
	ListenPort int    `json:"listenPort"`
}

type SecretRotation struct {
	Protocol Protocol `json:"protocol"`
	Field    string   `json:"field"`
	Value    string   `json:"value"`
}

type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Group    string   `json:"group"`
	Required bool     `json:"required"`
	Secret   bool     `json:"secret"`
	Options  []string `json:"options,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

type Schema struct {
	Type                    Type                `json:"type"`
	Fields                  []Field             `json:"fields"`
	Values                  map[string]any      `json:"defaults"`
	CertificateRequired     bool                `json:"certificateRequired"`
	CertificateRequiredWhen map[string][]string `json:"certificateRequiredWhen,omitempty"`
}

type PortSuggestion struct {
	ListenPort int `json:"listenPort"`
}
