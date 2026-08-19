package singbox

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterWarpParsesAccount(t *testing.T) {
	clientID := base64.StdEncoding.EncodeToString([]byte{0x1a, 0x69, 0x74, 0x00, 0xff})
	var receivedKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		receivedKey = body["key"]
		if _, err := base64.StdEncoding.DecodeString(body["key"]); err != nil {
			t.Errorf("request key is not base64: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"config": map[string]any{
				"client_id": clientID,
				"peers":     []map[string]string{{"public_key": testWarpKey('P')}},
				"interface": map[string]any{
					"addresses": map[string]string{"v4": "172.16.0.2/32", "v6": "2606:4700:110:8a12::/128"},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	service, _ := newTestService(t)
	service.warpRegisterURL = server.URL
	settings, err := service.RegisterWarp(t.Context())
	if err != nil {
		t.Fatalf("register warp: %v", err)
	}
	if settings.Endpoint != DefaultWarpEndpoint || settings.PublicKey != testWarpKey('P') {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.IPv4 != "172.16.0.2/32" || settings.IPv6 != "2606:4700:110:8a12::/128" {
		t.Fatalf("addresses = %#v", settings)
	}
	if !equalReserved(settings.Reserved, []int{0x1a, 0x69, 0x74}) {
		t.Fatalf("reserved = %#v", settings.Reserved)
	}
	if _, err := base64.StdEncoding.DecodeString(settings.PrivateKey); err != nil {
		t.Fatalf("private key not base64: %v", err)
	}
	if !validWireGuardKey(settings.PrivateKey) {
		t.Fatalf("private key not 32 bytes")
	}
	// 注册接口必须收到公钥而非私钥（曾把 private.Bytes() 当公钥发出去）。
	privateRaw, err := base64.StdEncoding.DecodeString(settings.PrivateKey)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateRaw)
	if err != nil {
		t.Fatalf("load private key: %v", err)
	}
	wantPublic := base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	if receivedKey != wantPublic {
		t.Fatalf("register sent %q, want public key %q", receivedKey, wantPublic)
	}
	if receivedKey == settings.PrivateKey {
		t.Fatal("register sent the private key as the public key")
	}
}

func TestParseWarpRegistrationAddsCIDRPrefix(t *testing.T) {
	body := `{"config":{"client_id":"IMPX","peers":[{"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="}],"interface":{"addresses":{"v4":"172.16.0.2","v6":"2606:4700:110:8187::"}}}}`
	settings, err := parseWarpRegistration([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if settings.IPv4 != "172.16.0.2/32" {
		t.Fatalf("ipv4 = %q, want /32 prefix", settings.IPv4)
	}
	if settings.IPv6 != "2606:4700:110:8187::/128" {
		t.Fatalf("ipv6 = %q, want /128 prefix", settings.IPv6)
	}
	if !equalReserved(settings.Reserved, []int{0x20, 0xc3, 0xd7}) {
		t.Fatalf("reserved = %#v", settings.Reserved)
	}
}

func TestRegisterWarpRejectsErrorResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	t.Cleanup(server.Close)
	service, _ := newTestService(t)
	service.warpRegisterURL = server.URL
	if _, err := service.RegisterWarp(t.Context()); err == nil {
		t.Fatal("error status unexpectedly accepted")
	}
}

func TestParseWarpRegistrationDefensive(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing peers", `{"config":{"client_id":"abcd","interface":{"addresses":{"v4":"172.16.0.2/32"}}}}`},
		{"missing v4", `{"config":{"peers":[{"public_key":"k"}],"interface":{"addresses":{"v6":"::/128"}}}}`},
		{"not json", `garbage`},
	}
	for _, testCase := range cases {
		if _, err := parseWarpRegistration([]byte(testCase.body)); err == nil {
			t.Fatalf("%s: unexpectedly accepted", testCase.name)
		}
	}
}

func TestWarpReservedFromClientIDFallbacks(t *testing.T) {
	if got := warpReservedFromClientID(""); got != nil {
		t.Fatalf("empty client id reserved = %#v", got)
	}
	if got := warpReservedFromClientID("ab"); got != nil {
		t.Fatalf("short client id reserved = %#v", got)
	}
	// 非 base64 的 client_id 退化为前三个原始字节。
	if got := warpReservedFromClientID("!!!!!!"); !equalReserved(got, []int{'!', '!', '!'}) {
		t.Fatalf("fallback reserved = %#v", got)
	}
}

func equalReserved(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

var _ = context.Background
