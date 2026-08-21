package share

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	serviceuser "github.com/keepon-online/sb-ops/internal/service/user"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

var (
	ErrTokenNotFound    = errors.New("订阅令牌不存在")
	ErrUserDisabled     = errors.New("订阅已停用")
	ErrUserExpired      = errors.New("订阅已过期")
	ErrQuotaExhausted   = errors.New("订阅流量已耗尽")
	ErrRuntimeNotReady  = errors.New("运行时配置尚未同步")
	ErrArtifactNotFound = errors.New("订阅产物不存在")
	// ErrDeviceDisabled 标记被管理员停用的设备：与用户级停用一样返回占位
	// 订阅，客户端表现为无法使用。
	ErrDeviceDisabled = errors.New("设备已停用")
)

type Artifact struct {
	Format        string
	ContentType   string
	Content       []byte
	Digest        string
	UsedBytes     int64
	QuotaBytes    int64
	ExpiresAt     *time.Time
	UserID        string
	DeviceID      string
	DeviceEnabled int
}

type Service struct {
	db    *sql.DB
	now   func() time.Time
	users *serviceuser.Service
	// sync 在自动发现新设备后触发一次编译同步，使新设备的入站用户与订阅
	// 产物立即可用；为空（测试）时跳过同步。
	sync func() error
	log  *slog.Logger
}

func NewService(db *sql.DB, users *serviceuser.Service, sync func() error) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }, users: users, sync: sync, log: slog.Default()}
}

// ArtifactByToken 解析订阅 token → 用户可用性检查 → 设备路由 → 设备维度的
// clash.yaml 产物。token 有两级：设备 token（已有设备的直连链接，不受 UA
// 影响）与用户 token（唯一订阅链接）：按请求 User-Agent + 来源 IP 路由到该
// 客户端的设备，首次出现的客户端自动创建设备并同步内核；同步失败或无法识别
// 客户端（空 UA）时回退该用户任一已有产物的设备（流量暂并入，客户端下次更新恢复按设备归因）。
func (s *Service) ArtifactByToken(ctx context.Context, token, format, userAgent string, sourceIPs ...string) (Artifact, error) {
	token = strings.TrimSpace(token)
	format = strings.ToLower(strings.TrimSpace(format))
	if token == "" {
		return Artifact{}, ErrTokenNotFound
	}
	if format == "" || strings.ContainsAny(format, `/\\`) {
		return Artifact{}, ErrArtifactNotFound
	}
	hash := sha256.Sum256([]byte(token))
	var artifact Artifact
	var userEnabled int
	var expiresAt common.Time
	var deviceUAKey string
	var direct bool
	err := s.db.QueryRowContext(ctx, `
SELECT u.id,u.enabled,d.enabled,u.used_bytes,u.quota_bytes,u.expires_at,d.id,d.ua_key
FROM devices d JOIN users u ON u.id=d.user_id
WHERE d.token_hash=?`, hash[:]).Scan(&artifact.UserID, &userEnabled, &artifact.DeviceEnabled, &artifact.UsedBytes, &artifact.QuotaBytes, &expiresAt, &artifact.DeviceID, &deviceUAKey)
	if errors.Is(err, sql.ErrNoRows) {
		// 设备 token 未命中：尝试用户级订阅链接。
		err = s.db.QueryRowContext(ctx, `
SELECT id,enabled,used_bytes,quota_bytes,expires_at
FROM users WHERE subscription_token_hash=?`, hash[:]).Scan(&artifact.UserID, &userEnabled, &artifact.UsedBytes, &artifact.QuotaBytes, &expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return Artifact{}, ErrTokenNotFound
		}
	}
	if err != nil {
		return Artifact{}, err
	}
	direct = deviceUAKey != "" || artifact.DeviceID != ""
	artifact.ExpiresAt = expiresAt.Ptr()
	now := s.now()
	uaInfo := ParseUserAgent(userAgent)
	sourceIP := ""
	if len(sourceIPs) > 0 {
		sourceIP = sourceIPs[0]
	}
	var seenAt *time.Time
	if uaInfo.Key != "" {
		seenAt = &now
	}
	if userEnabled == 0 {
		return Artifact{}, ErrUserDisabled
	}
	if artifact.ExpiresAt != nil && !artifact.ExpiresAt.After(now) {
		return Artifact{}, ErrUserExpired
	}
	if artifact.QuotaBytes > 0 && artifact.UsedBytes >= artifact.QuotaBytes {
		return Artifact{}, ErrQuotaExhausted
	}
	if direct {
		if artifact.DeviceEnabled == 0 {
			return Artifact{}, ErrDeviceDisabled
		}
	} else {
		// 用户链接：按 UA 路由到设备。
		if uaInfo.Key != "" {
			metadata := serviceuser.DeviceMetadata{Platform: uaInfo.Platform, Client: uaInfo.Client, ClientVersion: uaInfo.Version, SourceIP: sourceIP, LastSeenAt: seenAt}
			device, created, found, err := s.users.DeviceForKey(ctx, artifact.UserID, uaInfo.Key, uaInfo.Label, metadata)
			if err != nil {
				s.log.Warn("订阅自动建设备失败", "ua", uaInfo.Key, "error", err)
			}
			if err == nil && found {
				if !device.Enabled {
					return Artifact{}, ErrDeviceDisabled
				}
				if created {
					syncErr := s.applySync()
					if syncErr != nil {
						s.log.Warn("新建设备后同步内核失败", "device", device.ID, "ua", uaInfo.Key, "error", syncErr)
					} else {
						artifact.DeviceID = device.ID
						metadata := serviceuser.DeviceMetadata{Platform: uaInfo.Platform, Client: uaInfo.Client, ClientVersion: uaInfo.Version, SourceIP: sourceIP, LastSeenAt: seenAt}
						_ = s.users.UpdateDeviceMetadata(ctx, artifact.UserID, device.ID, metadata)
					}
				} else {
					artifact.DeviceID = device.ID
					metadata := serviceuser.DeviceMetadata{Platform: uaInfo.Platform, Client: uaInfo.Client, ClientVersion: uaInfo.Version, SourceIP: sourceIP, LastSeenAt: seenAt}
					_ = s.users.UpdateDeviceMetadata(ctx, artifact.UserID, device.ID, metadata)
				}
			}
		}
	}
	// 路由到特定设备后更新元数据（在路由分支内完成）——回退路径不更新。

	if artifact.DeviceID == "" {
		// 用户链接但尚未路由到设备（空 UA、达上限或同步失败）：回退该用户
		// 最近更新的任意设备产物，保持客户端可用。
		err = s.db.QueryRowContext(ctx, `
SELECT device_id,format,content,digest
FROM runtime_artifacts
WHERE user_id=? AND format=? ORDER BY updated_at DESC LIMIT 1`, artifact.UserID, format).Scan(&artifact.DeviceID, &artifact.Format, &artifact.Content, &artifact.Digest)
	} else {
		err = s.db.QueryRowContext(ctx, `
SELECT format,content,digest
FROM runtime_artifacts
WHERE user_id=? AND device_id=? AND format=?`, artifact.UserID, artifact.DeviceID, format).Scan(&artifact.Format, &artifact.Content, &artifact.Digest)
	}
	if errors.Is(err, sql.ErrNoRows) {
		var appliedAt common.Time
		if stateErr := s.db.QueryRowContext(ctx, `SELECT last_applied_at FROM runtime_state WHERE id=1`).Scan(&appliedAt); stateErr != nil && !errors.Is(stateErr, sql.ErrNoRows) {
			return Artifact{}, stateErr
		}
		if appliedAt.IsZero() {
			return Artifact{}, ErrRuntimeNotReady
		}
		return Artifact{}, ErrArtifactNotFound
	}
	if err != nil {
		return Artifact{}, err
	}
	artifact.ContentType = contentType(format)
	return artifact, nil
}

// applySync 触发一次编译同步（与请求生命周期解耦），未接线时视为成功。
func (s *Service) applySync() error {
	if s.sync == nil {
		return nil
	}
	return s.sync()
}

func (a Artifact) UserInfo() string {
	expires := int64(0)
	if a.ExpiresAt != nil {
		expires = a.ExpiresAt.Unix()
	}
	return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d", a.UsedBytes, a.QuotaBytes, expires)
}

func contentType(format string) string {
	switch {
	case strings.HasSuffix(format, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(format, ".yaml"), strings.HasSuffix(format, ".yml"):
		return "application/yaml; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}
