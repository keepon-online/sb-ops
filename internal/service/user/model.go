package user

import "time"

type Status string

const (
	StatusActive        Status = "active"
	StatusDisabled      Status = "disabled"
	StatusExpired       Status = "expired"
	StatusQuotaExceeded Status = "quotaExceeded"
)

// User 是订阅用户。UsedBytes 是驱动额度扣减的累计计数器：自开号（或上次
// 手动重置）起单调累加，与 traffic_hourly 的历史窗口统计（总览页口径）刻意
// 分离——重置/手工调整只改计数器不动历史，留存清理只删历史不动计数器，两边
// 数字不一致是预期行为而非采集丢失。
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
	SourceIP      string     `json:"sourceIp"`
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

// QuotaReset 记录一次周期额度重置：Before 是重置前快照（旧用量与状态判定
// 依据），NextResetAt 是推进后的下一轮重置边界（nil 表示脏数据自愈后不再
// 周期重置）。
type QuotaReset struct {
	Before      User
	NextResetAt *time.Time
}

// QuotaResetResult 汇总一轮周期额度重置：Reset 是全部被重置的用户，Recovered
// 是其中从额度超额恢复可用的——超额用户此前被编译剔除出协议入站，调用方应
// 触发一次内核同步让其凭据重新生效。
type QuotaResetResult struct {
	Reset     []QuotaReset
	Recovered []User
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
