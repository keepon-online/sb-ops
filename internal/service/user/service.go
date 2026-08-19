package user

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Service struct {
	store *Store
	now   func() time.Time
}

func NewService(store *Store) *Service {
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Create 建号只签发用户级订阅链接；设备由客户端导入链接时按 UA 自动创建
// （或管理员手动添加），面板不再预置“默认设备”。
func (s *Service) Create(ctx context.Context, input CreateInput) (User, string, error) {
	fields := validateInput(input.Name, input.QuotaBytes, input.ResetIntervalDays)
	if len(uniqueStrings(input.PolicyIDs)) > 1 {
		fields["policyIds"] = "must contain at most one subscription policy"
	}
	if len(fields) > 0 {
		return User{}, "", &common.ValidationError{Fields: fields}
	}
	if err := s.store.ValidateReferences(ctx, input.ProtocolIDs, input.PolicyIDs); err != nil {
		return User{}, "", err
	}
	now := s.now()
	value := User{
		ID: uuid.NewString(), Name: strings.TrimSpace(input.Name), Enabled: input.Enabled,
		QuotaBytes: input.QuotaBytes, ExpiresAt: input.ExpiresAt, ResetIntervalDays: input.ResetIntervalDays,
		ProtocolIDs: uniqueStrings(input.ProtocolIDs), PolicyIDs: uniqueStrings(input.PolicyIDs),
		DeviceCount: 0, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if value.ResetIntervalDays > 0 {
		next := now.AddDate(0, 0, value.ResetIntervalDays)
		value.NextResetAt = &next
	}
	token, err := s.store.IssueUserToken(ctx, value.ID, now)
	if err != nil {
		return User{}, "", err
	}
	value.Status = value.StatusAt(now)
	if err := s.store.Create(ctx, value, token); err != nil {
		return User{}, "", err
	}
	return value, token.Token, nil
}

func (s *Service) List(ctx context.Context, input ListInput) (Page, error) {
	return s.store.List(ctx, input)
}

func (s *Service) Get(ctx context.Context, id string) (User, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (User, error) {
	fields := validateInput(input.Name, input.QuotaBytes, input.ResetIntervalDays)
	if len(uniqueStrings(input.PolicyIDs)) > 1 {
		fields["policyIds"] = "must contain at most one subscription policy"
	}
	if input.Revision < 1 {
		fields["revision"] = "must be positive"
	}
	if len(fields) > 0 {
		return User{}, &common.ValidationError{Fields: fields}
	}
	if err := s.store.ValidateReferences(ctx, input.ProtocolIDs, input.PolicyIDs); err != nil {
		return User{}, err
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return User{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Enabled = input.Enabled
	current.QuotaBytes = input.QuotaBytes
	current.ExpiresAt = input.ExpiresAt
	current.ResetIntervalDays = input.ResetIntervalDays
	current.ProtocolIDs = uniqueStrings(input.ProtocolIDs)
	current.PolicyIDs = uniqueStrings(input.PolicyIDs)
	current.UpdatedAt = s.now()
	if current.ResetIntervalDays == 0 {
		current.NextResetAt = nil
	} else if current.NextResetAt == nil {
		next := current.UpdatedAt.AddDate(0, 0, current.ResetIntervalDays)
		current.NextResetAt = &next
	}
	if err := s.store.Update(ctx, current, input.Revision); err != nil {
		return User{}, err
	}
	return s.store.Get(ctx, input.ID)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func (s *Service) BatchStatus(ctx context.Context, ids []string, enabled bool) error {
	if len(ids) == 0 {
		return &common.ValidationError{Fields: map[string]string{"ids": "must not be empty"}}
	}
	return s.store.SetStatus(ctx, uniqueStrings(ids), enabled, s.now())
}

// SubscriptionToken 返回用户级订阅链接 token（存量用户惰性签发）。
func (s *Service) SubscriptionToken(ctx context.Context, userID string) (string, error) {
	return s.store.UserToken(ctx, userID)
}

// RotateSubscriptionToken 重签用户级订阅链接，旧链接立即失效（设备凭据不受影响）。
func (s *Service) RotateSubscriptionToken(ctx context.Context, userID string) (string, error) {
	return s.store.RotateUserToken(ctx, userID)
}

// ListDevices returns the user's devices with decrypted subscription tokens.
func (s *Service) ListDevices(ctx context.Context, userID string) ([]Device, error) {
	return s.store.ListDevices(ctx, userID)
}

// CreateDevice adds another device (independent credential + link) to a user.
func (s *Service) CreateDevice(ctx context.Context, userID string, input DeviceInput) (Device, IssuedCredential, error) {
	name := strings.TrimSpace(input.Name)
	fields := map[string]string{}
	if name == "" {
		fields["name"] = "is required"
	}
	if len(fields) > 0 {
		return Device{}, IssuedCredential{}, &common.ValidationError{Fields: fields}
	}
	if err := s.store.ensureUser(ctx, userID); err != nil {
		return Device{}, IssuedCredential{}, err
	}
	count, err := s.store.CountDevices(ctx, userID)
	if err != nil {
		return Device{}, IssuedCredential{}, err
	}
	if count >= MaxDevicesPerUser {
		return Device{}, IssuedCredential{}, &common.ValidationError{Fields: map[string]string{"name": fmt.Sprintf("每用户最多 %d 台设备", MaxDevicesPerUser)}}
	}
	now := s.now()
	issued, stored, err := s.issueDevice(ctx, userID, name, "", now)
	if err != nil {
		return Device{}, IssuedCredential{}, err
	}
	if err := s.store.CreateDevice(ctx, stored); err != nil {
		return Device{}, IssuedCredential{}, err
	}
	device := Device{ID: stored.ID, Name: name, Enabled: true, Platform: "unknown", Token: issued.Token, UUID: issued.UUID, RotatedAt: now, CreatedAt: now}
	return device, issued, nil
}

// DeviceForKey returns the auto-discovered device owning a subscription
// User-Agent fingerprint, creating it on first sight. found=false (with nil
// error) means the device cap is reached or the fingerprint is unknown and
// the caller should fall back to the default device; created distinguishes
// a fresh device (the caller should re-sync the kernel) from an existing one.
func (s *Service) DeviceForKey(ctx context.Context, userID, uaKey, label string, metadataValues ...DeviceMetadata) (device Device, created, found bool, err error) {
	var metadata DeviceMetadata
	if len(metadataValues) > 0 {
		metadata = metadataValues[0]
	}
	if uaKey == "" {
		return Device{}, false, false, nil
	}
	if existing, ok, err := s.store.FindDeviceByUAKey(ctx, userID, uaKey); err != nil || ok {
		if err != nil || !ok {
			return existing, false, ok, err
		}
		_ = s.store.UpdateDeviceMetadata(ctx, userID, existing.ID, metadata, 5*time.Minute)
		return existing, false, true, nil
	}
	count, err := s.store.CountDevices(ctx, userID)
	if err != nil {
		return Device{}, false, false, err
	}
	if count >= MaxDevicesPerUser {
		return Device{}, false, false, nil
	}
	now := s.now()
	issued, stored, err := s.issueDevice(ctx, userID, label, uaKey, now)
	if err != nil {
		return Device{}, false, false, err
	}
	stored.Platform, stored.Client, stored.ClientVersion, stored.LastSeenAt = metadata.Platform, metadata.Client, metadata.ClientVersion, metadata.LastSeenAt
	if err := s.store.CreateDevice(ctx, stored); err != nil {
		// 并发首次下载同一客户端：唯一索引兜底，改为返回既有设备。
		if existing, ok, findErr := s.store.FindDeviceByUAKey(ctx, userID, uaKey); ok && findErr == nil {
			return existing, false, true, nil
		}
		return Device{}, false, false, err
	}
	device = Device{ID: stored.ID, Name: label, Enabled: true, Auto: true, Platform: stored.Platform, Client: stored.Client, ClientVersion: stored.ClientVersion, LastSeenAt: stored.LastSeenAt, UUID: issued.UUID, RotatedAt: now, CreatedAt: now}
	return device, true, true, nil
}

// RotateDevice re-issues one device's credential; the old token/uuid/password
// die immediately.
func (s *Service) RotateDevice(ctx context.Context, userID, deviceID string) (Device, IssuedCredential, error) {
	devices, err := s.store.ListDevices(ctx, userID)
	if err != nil {
		return Device{}, IssuedCredential{}, err
	}
	var current *Device
	for index := range devices {
		if devices[index].ID == deviceID {
			current = &devices[index]
			break
		}
	}
	if current == nil {
		return Device{}, IssuedCredential{}, common.ErrNotFound
	}
	now := s.now()
	issued, stored, err := s.issueDevice(ctx, userID, current.Name, "", now)
	if err != nil {
		return Device{}, IssuedCredential{}, err
	}
	stored.ID = deviceID
	if err := s.store.RotateDevice(ctx, userID, deviceID, stored); err != nil {
		return Device{}, IssuedCredential{}, err
	}
	value := *current
	value.Token, value.UUID, value.RotatedAt = issued.Token, issued.UUID, now
	return value, issued, nil
}

// UpdateDeviceMetadata records the latest observed subscription client.
func (s *Service) UpdateDeviceMetadata(ctx context.Context, userID, deviceID string, metadata DeviceMetadata) error {
	return s.store.UpdateDeviceMetadata(ctx, userID, deviceID, metadata, 5*time.Minute)
}

// SetDeviceEnabled enables/disables one device (disabled devices are dropped
// from inbounds and their links serve a placeholder).
func (s *Service) SetDeviceEnabled(ctx context.Context, userID, deviceID string, enabled bool) error {
	return s.store.SetDeviceEnabled(ctx, userID, deviceID, enabled)
}

// DeleteDevice removes one device entirely.
func (s *Service) DeleteDevice(ctx context.Context, userID, deviceID string) error {
	return s.store.DeleteDevice(ctx, userID, deviceID)
}

func (s *Service) ResetTraffic(ctx context.Context, id string) (User, error) {
	if err := s.store.ResetTraffic(ctx, id, s.now()); err != nil {
		return User{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) AdjustQuota(ctx context.Context, id string, input TrafficInput) (User, error) {
	if err := s.store.AdjustQuota(ctx, id, input.Mode, input.Bytes, s.now()); err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) AdjustTraffic(ctx context.Context, id string, input TrafficInput) (User, error) {
	if err := s.store.AdjustTraffic(ctx, id, input.Mode, input.Bytes, s.now()); err != nil {
		return User{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) issueDevice(ctx context.Context, userID, name, uaKey string, now time.Time) (IssuedCredential, storedDevice, error) {
	token, err := randomHex(24)
	if err != nil {
		return IssuedCredential{}, storedDevice{}, err
	}
	password, err := randomHex(16)
	if err != nil {
		return IssuedCredential{}, storedDevice{}, err
	}
	deviceID := uuid.NewString()
	issued := IssuedCredential{DeviceID: deviceID, Token: token, UUID: uuid.NewString(), Password: password}
	stored, err := newStoredDevice(ctx, s.store, deviceID, userID, name, uaKey, token, issued.UUID, password, now)
	return issued, stored, err
}

func validateInput(name string, quotaBytes int64, resetIntervalDays int) map[string]string {
	fields := map[string]string{}
	if strings.TrimSpace(name) == "" {
		fields["name"] = "is required"
	}
	if quotaBytes < 0 {
		fields["quotaBytes"] = "must not be negative"
	}
	if resetIntervalDays < 0 {
		fields["resetIntervalDays"] = "must not be negative"
	}
	return fields
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
