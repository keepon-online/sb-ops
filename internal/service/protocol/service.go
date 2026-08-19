package protocol

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

const maskedSecret = "********"

type Service struct {
	store *Store
	now   func() time.Time
}

func NewService(store *Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Schema(value Type) (Schema, error) {
	schema, ok := SchemaFor(value)
	if !ok {
		return Schema{}, &common.ValidationError{Fields: map[string]string{"type": "unsupported protocol type"}}
	}
	return schema, nil
}

func (s *Service) SuggestPort(ctx context.Context, value Type) (PortSuggestion, error) {
	if _, ok := SchemaFor(value); !ok {
		return PortSuggestion{}, &common.ValidationError{Fields: map[string]string{"type": "unsupported protocol type"}}
	}
	ports, err := s.store.UsedListenPorts(ctx, transportFor(value))
	if err != nil {
		return PortSuggestion{}, err
	}
	// 443 是特权端口(需 root 才能监听)且常被运营商/GFW 重点关照，不作默认推荐；
	// 优先推荐 8443 与 Cloudflare 兼容端口，其次 2443-2448，最后顺序扫描 1024+。
	for _, port := range []int{8443, 2053, 2083, 2087, 2096, 2443, 2444, 2445, 2446, 2447, 2448} {
		if !ports[port] {
			return PortSuggestion{ListenPort: port}, nil
		}
	}
	for port := 1024; port <= 65535; port++ {
		if !ports[port] {
			return PortSuggestion{ListenPort: port}, nil
		}
	}
	return PortSuggestion{}, &common.ValidationError{Fields: map[string]string{"listenPort": "no available port"}}
}

func transportFor(value Type) string {
	if value == TypeHysteria2 || value == TypeTUIC {
		return "udp"
	}
	return "tcp"
}

func (s *Service) Create(ctx context.Context, input WriteInput) (Protocol, error) {
	value, fields, err := s.prepare(ctx, input, nil)
	if err != nil {
		return Protocol{}, err
	}
	if len(fields) > 0 {
		return Protocol{}, &common.ValidationError{Fields: fields}
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.Create(ctx, value); err != nil {
		return Protocol{}, err
	}
	return mask(value), nil
}

func (s *Service) List(ctx context.Context) ([]Protocol, error) {
	values, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	for index := range values {
		values[index] = mask(values[index])
	}
	return values, nil
}

func (s *Service) Get(ctx context.Context, id string) (Protocol, error) {
	value, err := s.store.Get(ctx, id)
	return mask(value), err
}

func (s *Service) Update(ctx context.Context, input WriteInput) (Protocol, error) {
	if input.Revision < 1 {
		return Protocol{}, &common.ValidationError{Fields: map[string]string{"revision": "must be positive"}}
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Protocol{}, err
	}
	value, fields, err := s.prepare(ctx, input, &current)
	if err != nil {
		return Protocol{}, err
	}
	if len(fields) > 0 {
		return Protocol{}, &common.ValidationError{Fields: fields}
	}
	value.ID, value.CreatedAt, value.UpdatedAt = current.ID, current.CreatedAt, s.now()
	if err := s.store.Update(ctx, value, input.Revision); err != nil {
		return Protocol{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *Service) Delete(ctx context.Context, id string) error { return s.store.Delete(ctx, id) }

func (s *Service) Clone(ctx context.Context, id string, input CloneInput) (Protocol, error) {
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return Protocol{}, err
	}
	return s.Create(ctx, WriteInput{
		Name: input.Name, Type: current.Type, Enabled: false, Listen: current.Listen,
		ListenPort: input.ListenPort, Settings: current.Settings, CertificateID: current.CertificateID,
	})
}

func (s *Service) RotateSecret(ctx context.Context, id string) (SecretRotation, error) {
	current, err := s.store.Get(ctx, id)
	if err != nil {
		return SecretRotation{}, err
	}
	field := "password"
	var value string
	if current.Type == TypeVLESS && stringValue(current.Settings["tlsMode"]) == "reality" {
		field = "realityPrivateKey"
		privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return SecretRotation{}, err
		}
		value = base64.RawURLEncoding.EncodeToString(privateKey.Bytes())
		current.Settings[field] = value
		current.Settings["realityPublicKey"] = base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	} else if current.Type == TypeShadowsocks {
		value, err = randomBase64(32)
		if err != nil {
			return SecretRotation{}, err
		}
		current.Settings[field] = value
	} else if current.Type == TypeVMess {
		return SecretRotation{}, &common.ValidationError{Fields: map[string]string{"type": "vmess has no server secret"}}
	} else {
		value, err = randomHex(16)
		if err != nil {
			return SecretRotation{}, err
		}
		current.Settings[field] = value
	}
	current.UpdatedAt = s.now()
	if err := s.store.Update(ctx, current, current.Revision); err != nil {
		return SecretRotation{}, err
	}
	updated, err := s.Get(ctx, id)
	return SecretRotation{Protocol: updated, Field: field, Value: value}, err
}

func (s *Service) prepare(ctx context.Context, input WriteInput, current *Protocol) (Protocol, map[string]string, error) {
	fields := map[string]string{}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		fields["name"] = "is required"
	}
	schema, ok := SchemaFor(input.Type)
	if !ok {
		fields["type"] = "unsupported protocol type"
		return Protocol{}, fields, nil
	}
	listen := strings.TrimSpace(input.Listen)
	if listen == "" {
		listen = "::"
	}
	if input.ListenPort < 1 || input.ListenPort > 65535 {
		fields["listenPort"] = "must be between 1 and 65535"
	}
	settings := cloneMap(schema.Values)
	for key, value := range input.Settings {
		settings[key] = value
	}
	if current != nil {
		for _, field := range schema.Fields {
			if field.Secret && (stringValue(settings[field.Name]) == "" || stringValue(settings[field.Name]) == maskedSecret) {
				settings[field.Name] = current.Settings[field.Name]
			}
		}
	}
	for _, field := range schema.Fields {
		if field.Required && isEmpty(settings[field.Name]) {
			fields["settings."+field.Name] = "is required"
		}
		if len(field.Options) > 0 && !isEmpty(settings[field.Name]) && !contains(field.Options, stringValue(settings[field.Name])) {
			fields["settings."+field.Name] = "has an unsupported value"
		}
	}
	if input.Type == TypeHysteria2 {
		for _, key := range []string{"upMbps", "downMbps"} {
			if numberValue(settings[key]) <= 0 {
				fields["settings."+key] = "must be positive"
			}
		}
	}
	if input.Type == TypeHysteria2 || input.Type == TypeTUIC {
		from, to := int(numberValue(settings["portHopFrom"])), int(numberValue(settings["portHopTo"]))
		if from != 0 || to != 0 {
			if from < 1 || from > 65535 || to < 1 || to > 65535 || from > to {
				fields["settings.portHopFrom"] = "must form a valid 1-65535 port range"
			}
		}
	}
	certificateRequired := input.Type == TypeHysteria2 || input.Type == TypeTUIC || input.Type == TypeAnyTLS ||
		(input.Type == TypeVLESS && stringValue(settings["tlsMode"]) == "certificate") ||
		(input.Type == TypeVMess && stringValue(settings["tlsMode"]) == "certificate")
	if certificateRequired && (input.CertificateID == nil || strings.TrimSpace(*input.CertificateID) == "") {
		fields["certificateId"] = "is required for this protocol"
	}
	if input.CertificateID != nil && strings.TrimSpace(*input.CertificateID) != "" {
		exists, err := s.store.CertificateExists(ctx, strings.TrimSpace(*input.CertificateID))
		if err != nil {
			return Protocol{}, nil, err
		}
		if !exists {
			fields["certificateId"] = "references an unknown certificate"
		}
	}
	if (input.Type == TypeVLESS || input.Type == TypeTrojan) && stringValue(settings["tlsMode"]) == "reality" && stringValue(settings["realityPrivateKey"]) == "" {
		privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return Protocol{}, nil, err
		}
		settings["realityPrivateKey"] = base64.RawURLEncoding.EncodeToString(privateKey.Bytes())
		settings["realityPublicKey"] = base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	}
	if (input.Type == TypeVLESS || input.Type == TypeTrojan) && stringValue(settings["tlsMode"]) == "reality" {
		shortID := stringValue(settings["shortId"])
		if shortID == "" {
			generated, err := randomHex(4)
			if err != nil {
				return Protocol{}, nil, err
			}
			settings["shortId"] = generated
		} else if _, err := hex.DecodeString(shortID); err != nil || len(shortID) > 8 {
			fields["settings.shortId"] = "must contain at most 8 hexadecimal characters"
		}
	}
	if input.Type != TypeVMess && input.Type != TypeVLESS && stringValue(settings["password"]) == "" {
		generated, err := randomHex(16)
		if input.Type == TypeShadowsocks {
			keyLength := shadowsocksKeyLength(stringValue(settings["method"]))
			generated, err = randomBase64(keyLength)
		}
		if err != nil {
			return Protocol{}, nil, err
		}
		settings["password"] = generated
	}
	if input.Type == TypeShadowsocks {
		if _, err := shadowsocksPassword(stringValue(settings["method"]), stringValue(settings["password"])); err != nil {
			fields["settings.password"] = err.Error()
		}
	}
	return Protocol{Name: name, Type: input.Type, Enabled: input.Enabled, Listen: listen, ListenPort: input.ListenPort, Settings: settings, CertificateID: input.CertificateID}, fields, nil
}

func mask(value Protocol) Protocol {
	value.Settings = cloneMap(value.Settings)
	schema, _ := SchemaFor(value.Type)
	for _, field := range schema.Fields {
		if field.Secret && !isEmpty(value.Settings[field.Name]) {
			value.Settings[field.Name] = maskedSecret
		}
	}
	return value
}

func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func stringValue(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }

func numberValue(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func randomHex(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func randomBase64(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buffer), nil
}

func shadowsocksKeyLength(method string) int {
	if method == "2022-blake3-aes-128-gcm" {
		return 16
	}
	return 32
}

func shadowsocksPassword(method, password string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(password)
	if err != nil {
		return nil, errors.New("必须是标准 Base64 编码")
	}
	if len(value) != shadowsocksKeyLength(method) {
		return nil, fmt.Errorf("必须解码为 %d 字节（%s）", shadowsocksKeyLength(method), method)
	}
	return value, nil
}
