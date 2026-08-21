package user

import "time"

type Status string

const (
	StatusActive        Status = "active"
	StatusDisabled      Status = "disabled"
	StatusExpired       Status = "expired"
	StatusQuotaExceeded Status = "quotaExceeded"
)

type User struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Enabled           bool       `json:"enabled"`
	Status            Status     `json:"status"`
	QuotaBytes        int64      `json:"quotaBytes"`
	UsedBytes         int64      `json:"usedBytes"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	ResetIntervalDays int        `json:"resetIntervalDays"`
	NextResetAt       *time.Time `json:"nextResetAt,omitempty"`
	ProtocolIDs       []string   `json:"protocolIds"`
	PolicyIDs         []string   `json:"policyIds"`
	DeviceCount       int        `json:"deviceCount"`
	Revision          int64      `json:"revision"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// Device 是用户的一台接入设备：一份独立凭据（uuid/password/订阅 token），
// 入站用户 name 用设备 id 做 V2Ray stats 归因标签。Auto 标记由订阅链接
// 的 UA + 来源地址自动发现的设备。
type Device struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Enabled       bool       `json:"enabled"`
	UsedBytes     int64      `json:"usedBytes"`
	Token         string     `json:"token"`
	UUID          string     `json:"uuid"`
	Auto          bool       `json:"auto"`
	Platform      string     `json:"platform"`
	Client        string     `json:"client"`
	ClientVersion string     `json:"clientVersion"`
	LastSeenAt    *time.Time `json:"lastSeenAt,omitempty"`
	RotatedAt     time.Time  `json:"rotatedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// IssuedCredential 是一次性明文展示的新签发凭据（设备维度）。
type IssuedCredential struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	UUID     string `json:"uuid"`
	Password string `json:"password"`
}

func (u User) StatusAt(now time.Time) Status {
	if !u.Enabled {
		return StatusDisabled
	}
	if u.ExpiresAt != nil && !u.ExpiresAt.After(now) {
		return StatusExpired
	}
	if u.QuotaBytes > 0 && u.UsedBytes >= u.QuotaBytes {
		return StatusQuotaExceeded
	}
	return StatusActive
}

type ListInput struct {
	Cursor string
	Limit  int
	Query  string
	Status Status
	Sort   string
}

type Page struct {
	Items      []User `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type CreateInput struct {
	Name              string
	Enabled           bool
	QuotaBytes        int64
	ExpiresAt         *time.Time
	ResetIntervalDays int
	ProtocolIDs       []string
	PolicyIDs         []string
}

type UpdateInput struct {
	ID                string
	Revision          int64
	Name              string
	Enabled           bool
	QuotaBytes        int64
	ExpiresAt         *time.Time
	ResetIntervalDays int
	ProtocolIDs       []string
	PolicyIDs         []string
}

type TrafficInput struct {
	Mode  string
	Bytes int64
}

// DeviceMetadata contains the latest client observation from a subscription request.
type DeviceMetadata struct {
	Platform      string
	Client        string
	ClientVersion string
	SourceIP      string
	LastSeenAt    *time.Time
}

// MaxDevicesPerUser caps inbound user entries per user (each device becomes one
// entry in every assigned inbound and one V2Ray stats whitelist entry).
const MaxDevicesPerUser = 10
