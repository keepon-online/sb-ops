package protocol

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestPrepareVLESSRealityGeneratesShortID(t *testing.T) {
	value, fields, err := NewService(nil).prepare(context.Background(), WriteInput{
		Name: "VLESS Reality", Type: TypeVLESS, Enabled: true, Listen: "::", ListenPort: 2443,
		Settings: map[string]any{"transport": "tcp", "tlsMode": "reality", "serverName": "www.microsoft.com"},
	}, nil)
	if err != nil {
		t.Fatalf("prepare protocol: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("validation fields = %v", fields)
	}
	shortID := stringValue(value.Settings["shortId"])
	if len(shortID) != 8 {
		t.Fatalf("shortId length = %d, want 8", len(shortID))
	}
	if _, err := hex.DecodeString(shortID); err != nil {
		t.Fatalf("shortId is not hexadecimal: %v", err)
	}
}

func TestPrepareShadowsocks128Generates16BytePassword(t *testing.T) {
	value, fields, err := NewService(nil).prepare(context.Background(), WriteInput{
		Name: "SS-2022", Type: TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444,
		Settings: map[string]any{"method": "2022-blake3-aes-128-gcm"},
	}, nil)
	if err != nil {
		t.Fatalf("prepare protocol: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("validation fields = %v", fields)
	}
	raw, err := base64.StdEncoding.DecodeString(stringValue(value.Settings["password"]))
	if err != nil {
		t.Fatalf("decode password: %v", err)
	}
	if len(raw) != 16 {
		t.Fatalf("password length = %d, want 16", len(raw))
	}
}

func TestPrepareShadowsocksRejectsInvalidServerPassword(t *testing.T) {
	_, fields, err := NewService(nil).prepare(context.Background(), WriteInput{
		Name: "SS-2022", Type: TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444,
		Settings: map[string]any{"method": "2022-blake3-aes-128-gcm", "password": "not-base64"},
	}, nil)
	if err != nil {
		t.Fatalf("prepare protocol: %v", err)
	}
	if fields["settings.password"] != "必须是标准 Base64 编码" {
		t.Fatalf("validation fields = %v", fields)
	}
}

func TestPrepareTrojanRealityGeneratesKey(t *testing.T) {
	value, fields, err := NewService(nil).prepare(context.Background(), WriteInput{
		Name: "Trojan Reality", Type: TypeTrojan, Enabled: true, Listen: "::", ListenPort: 2445,
		Settings: map[string]any{"tlsMode": "reality", "serverName": "www.microsoft.com"},
	}, nil)
	if err != nil {
		t.Fatalf("prepare protocol: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("validation fields = %v", fields)
	}
	privateKey := stringValue(value.Settings["realityPrivateKey"])
	publicKey := stringValue(value.Settings["realityPublicKey"])
	shortID := stringValue(value.Settings["shortId"])
	if _, err := base64.RawURLEncoding.DecodeString(privateKey); err != nil {
		t.Fatalf("realityPrivateKey = %q, not valid RawURLEncoding base64: %v", privateKey, err)
	}
	if len(publicKey) == 0 {
		t.Fatalf("realityPublicKey is empty")
	}
	if len(shortID) != 8 {
		t.Fatalf("shortId length = %d, want 8", len(shortID))
	}
}

func TestPrepareShadowsocksRejectsWrongServerPasswordLength(t *testing.T) {
	_, fields, err := NewService(nil).prepare(context.Background(), WriteInput{
		Name: "SS-2022", Type: TypeShadowsocks, Enabled: true, Listen: "::", ListenPort: 2444,
		Settings: map[string]any{"method": "2022-blake3-aes-256-gcm", "password": base64.StdEncoding.EncodeToString(make([]byte, 16))},
	}, nil)
	if err != nil {
		t.Fatalf("prepare protocol: %v", err)
	}
	if fields["settings.password"] != "必须解码为 32 字节（2022-blake3-aes-256-gcm）" {
		t.Fatalf("validation fields = %v", fields)
	}
}

func TestSchemaForClonesCertificateRequiredWhen(t *testing.T) {
	// 回归：克隆循环曾 range 刚替换的空目标 map，导致证书条件丢失，
	// 前端拿不到 certificateRequiredWhen 就不渲染 TLS 证书选择器。
	for _, value := range []Type{TypeVLESS, TypeVMess, TypeTrojan} {
		schema, ok := SchemaFor(value)
		if !ok {
			t.Fatalf("schema for %s missing", value)
		}
		when := schema.CertificateRequiredWhen["tlsMode"]
		if len(when) != 1 || when[0] != "certificate" {
			t.Fatalf("%s certificateRequiredWhen[tlsMode] = %v, want [certificate]", value, when)
		}
		// 返回值必须是副本：改动不能影响后续调用。
		schema.CertificateRequiredWhen["tlsMode"][0] = "mutated"
		again, _ := SchemaFor(value)
		if got := again.CertificateRequiredWhen["tlsMode"][0]; got != "certificate" {
			t.Fatalf("%s schema shared backing slice: got %q", value, got)
		}
	}
}

func TestProtocolInboundTagDeduplication(t *testing.T) {
	cases := []struct {
		name     string
		proto    Protocol
		expected string
	}{
		{
			name:     "already prefixed with type and dash",
			proto:    Protocol{Type: TypeVLESS, Name: "vless-grpc-reality"},
			expected: "vless-grpc-reality",
		},
		{
			name:     "exact type match",
			proto:    Protocol{Type: TypeShadowsocks, Name: "shadowsocks"},
			expected: "shadowsocks",
		},
		{
			name:     "vmess prefixed",
			proto:    Protocol{Type: TypeVMess, Name: "vmess-ws"},
			expected: "vmess-ws",
		},
		{
			name:     "no prefix gets type prepended",
			proto:    Protocol{Type: TypeVLESS, Name: "grpc-reality"},
			expected: "vless-grpc-reality",
		},
		{
			name:     "hysteria2 short name gets type prepended",
			proto:    Protocol{Type: TypeHysteria2, Name: "h2"},
			expected: "hysteria2-h2",
		},
		{
			name:     "empty name falls back to ID with prefix",
			proto:    Protocol{Type: TypeTrojan, ID: "proto-123"},
			expected: "trojan-proto-123",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.proto.InboundTag()
			if got != tc.expected {
				t.Fatalf("InboundTag() = %q, want %q", got, tc.expected)
			}
		})
	}
}
