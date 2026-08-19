package argo

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
	serviceprotocol "github.com/keepon-online/sb-ops/internal/service/protocol"
)

type Service struct {
	store     *Store
	protocols *serviceprotocol.Store
	now       func() time.Time
}

func NewService(store *Store, protocols *serviceprotocol.Store) *Service {
	return &Service{store: store, protocols: protocols, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) List(ctx context.Context) ([]Tunnel, error) {
	return s.store.List(ctx)
}

func (s *Service) Create(ctx context.Context, input TunnelWriteInput) (Tunnel, error) {
	value, fields := s.tunnelFromInput(ctx, input)
	if len(fields) > 0 {
		return Tunnel{}, &common.ValidationError{Fields: fields}
	}
	if input.Mode == ModeToken && (input.Token == nil || strings.TrimSpace(*input.Token) == "") {
		return Tunnel{}, validation("token", "is required for token tunnels")
	}
	now := s.now()
	value.ID, value.Revision, value.CreatedAt, value.UpdatedAt = uuid.NewString(), 1, now, now
	if err := s.store.Create(ctx, value, input.Token); err != nil {
		return Tunnel{}, err
	}
	return s.store.Get(ctx, value.ID)
}

func (s *Service) Update(ctx context.Context, input TunnelWriteInput) (Tunnel, error) {
	if input.Revision < 1 {
		return Tunnel{}, validation("revision", "must be positive")
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Tunnel{}, err
	}
	value, fields := s.tunnelFromInput(ctx, input)
	if len(fields) > 0 {
		return Tunnel{}, &common.ValidationError{Fields: fields}
	}
	if input.Mode == ModeToken {
		if input.Token != nil && strings.TrimSpace(*input.Token) == "" {
			return Tunnel{}, validation("token", "cannot be emptied; switch to temporary mode instead")
		}
		if input.Token == nil && !current.TokenSet {
			return Tunnel{}, validation("token", "is required for token tunnels")
		}
	}
	value.ID, value.CreatedAt = current.ID, current.CreatedAt
	value.UpdatedAt = s.now()
	if err := s.store.Update(ctx, value, input.Revision, input.Token); err != nil {
		return Tunnel{}, err
	}
	return s.store.Get(ctx, input.ID)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// tunnelFromInput validates the admin-facing fields. protocolID must point at
// an enabled vmess protocol: Argo rides a plain WebSocket origin, which the
// vmess inbound always provides (vless forces TLS/reality and cannot ride a
// plain HTTP tunnel origin).
func (s *Service) tunnelFromInput(ctx context.Context, input TunnelWriteInput) (Tunnel, map[string]string) {
	fields := map[string]string{}
	value := Tunnel{
		ID: input.ID, Name: strings.TrimSpace(input.Name), Enabled: input.Enabled,
		Mode:        strings.ToLower(strings.TrimSpace(input.Mode)),
		FixedDomain: strings.TrimSpace(input.FixedDomain), PreferredAddress: strings.TrimSpace(input.PreferredAddress),
		ProtocolID: strings.TrimSpace(input.ProtocolID),
	}
	if value.Name == "" {
		fields["name"] = "is required"
	}
	switch value.Mode {
	case ModeTemporary:
	case ModeToken:
		if value.FixedDomain == "" {
			fields["fixedDomain"] = "is required for token tunnels"
		}
	default:
		fields["mode"] = "must be temporary or token"
	}
	if value.ProtocolID == "" {
		fields["protocolId"] = "is required"
	} else {
		protocol, err := s.protocols.Get(ctx, value.ProtocolID)
		if err != nil {
			fields["protocolId"] = "must reference an existing protocol"
		} else if protocol.Type != serviceprotocol.TypeVMess || !protocol.Enabled {
			fields["protocolId"] = "must be an enabled vmess protocol（Argo 隧道走 vmess+ws）"
		}
	}
	return value, fields
}

// validation is a small helper matching the singbox service style.
func validation(field, message string) *common.ValidationError {
	return &common.ValidationError{Fields: map[string]string{field: message}}
}
