package argo

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	resource "github.com/keepon-online/sb-ops/internal/service"
	"github.com/keepon-online/sb-ops/internal/service/common"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	_ "github.com/mattn/go-sqlite3"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, schema := range []string{resource.Schema, Schema} {
		if _, err := db.Exec(schema); err != nil {
			t.Fatalf("apply schema: %v", err)
		}
	}
	secrets := secret.NewStore(db)
	protocols := serviceprotocol.NewStore(db, secrets)
	return NewService(NewStore(db, secrets), protocols)
}

func seedProtocol(t *testing.T, service *Service, id string, protocolType serviceprotocol.Type, enabled bool, port int) {
	t.Helper()
	value := serviceprotocol.Protocol{
		ID: id, Name: id, Type: protocolType, Enabled: enabled, Listen: "::", ListenPort: port,
		Settings: map[string]any{"transport": "ws", "wsPath": "/ws"}, Revision: 1,
	}
	if err := service.protocols.Create(context.Background(), value); err != nil {
		t.Fatalf("seed protocol %s: %v", id, err)
	}
}

func TestTunnelValidation(t *testing.T) {
	service := newTestService(t)
	seedProtocol(t, service, "vm", "vmess", true, 2096)
	seedProtocol(t, service, "off", "vmess", false, 2097)
	seedProtocol(t, service, "vl", "vless", true, 2098)

	token := "tunnel-token"
	cases := []struct {
		name   string
		input  TunnelWriteInput
		fields []string
	}{
		{"token mode without domain", TunnelWriteInput{Name: "a", Enabled: true, Mode: ModeToken, ProtocolID: "vm", Token: &token}, []string{"fixedDomain"}},
		{"token mode without token", TunnelWriteInput{Name: "b", Enabled: true, Mode: ModeToken, FixedDomain: "t.example.com", ProtocolID: "vm"}, []string{"token"}},
		{"vless protocol", TunnelWriteInput{Name: "c", Enabled: true, Mode: ModeTemporary, ProtocolID: "vl"}, []string{"protocolId"}},
		{"disabled protocol", TunnelWriteInput{Name: "d", Enabled: true, Mode: ModeTemporary, ProtocolID: "off"}, []string{"protocolId"}},
		{"unknown protocol", TunnelWriteInput{Name: "e", Enabled: true, Mode: ModeTemporary, ProtocolID: "missing"}, []string{"protocolId"}},
		{"bad mode", TunnelWriteInput{Name: "f", Enabled: true, Mode: "grpc", ProtocolID: "vm"}, []string{"mode"}},
		{"empty name", TunnelWriteInput{Enabled: true, Mode: ModeTemporary, ProtocolID: "vm"}, []string{"name"}},
	}
	for _, testCase := range cases {
		_, err := service.Create(context.Background(), testCase.input)
		var validation *common.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%s: err = %v, want validation error", testCase.name, err)
		}
		for _, field := range testCase.fields {
			if validation.Fields[field] == "" {
				t.Fatalf("%s: fields = %#v, want %q", testCase.name, validation.Fields, field)
			}
		}
	}
}

func TestTunnelTokenEncryptedAndKeptOnUpdate(t *testing.T) {
	service := newTestService(t)
	seedProtocol(t, service, "vm", "vmess", true, 2096)
	token := "tunnel-token"
	created, err := service.Create(context.Background(), TunnelWriteInput{Name: "argo", Enabled: true, Mode: ModeToken, FixedDomain: "t.example.com", ProtocolID: "vm", Token: &token})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Token != "" || !created.TokenSet {
		t.Fatalf("created tunnel should mask token: %#v", created)
	}
	var stored []byte
	if err := service.store.db.QueryRow(`SELECT token_ciphertext FROM argo_tunnels WHERE id=?`, created.ID).Scan(&stored); err != nil {
		t.Fatalf("load token: %v", err)
	}
	if len(stored) == 0 || string(stored) == token {
		t.Fatal("token missing or stored in plaintext")
	}
	// 留空 token 更新沿用旧值。
	updated, err := service.Update(context.Background(), TunnelWriteInput{ID: created.ID, Revision: created.Revision, Name: "argo", Enabled: true, Mode: ModeToken, FixedDomain: "t.example.com", ProtocolID: "vm"})
	if err != nil {
		t.Fatalf("update without token: %v", err)
	}
	if !updated.TokenSet {
		t.Fatal("token should be preserved on update")
	}
	// 同协议第二条启用隧道被唯一索引拒绝。
	second := "another-token"
	if _, err := service.Create(context.Background(), TunnelWriteInput{Name: "argo2", Enabled: true, Mode: ModeTemporary, ProtocolID: "vm", Token: &second}); err == nil {
		t.Fatal("duplicate enabled protocol unexpectedly accepted")
	}
	// 禁用后可再建。
	if _, err := service.Update(context.Background(), TunnelWriteInput{ID: created.ID, Revision: updated.Revision, Name: "argo", Enabled: false, Mode: ModeToken, FixedDomain: "t.example.com", ProtocolID: "vm"}); err != nil {
		t.Fatalf("disable tunnel: %v", err)
	}
	if _, err := service.Create(context.Background(), TunnelWriteInput{Name: "argo2", Enabled: true, Mode: ModeTemporary, ProtocolID: "vm"}); err != nil {
		t.Fatalf("create after disable: %v", err)
	}
}

func TestUpdateRuntimePersistsSupervisorState(t *testing.T) {
	service := newTestService(t)
	seedProtocol(t, service, "vm", "vmess", true, 2096)
	created, err := service.Create(context.Background(), TunnelWriteInput{Name: "argo", Enabled: true, Mode: ModeTemporary, ProtocolID: "vm"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.store.UpdateRuntime(context.Background(), created.ID, RuntimeUpdate{AssignedDomain: "word.trycloudflare.com", Status: StatusRunning, LastError: ""}, service.now()); err != nil {
		t.Fatalf("update runtime: %v", err)
	}
	value, err := service.store.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if value.AssignedDomain != "word.trycloudflare.com" || value.Status != StatusRunning {
		t.Fatalf("tunnel runtime = %#v", value)
	}
}
