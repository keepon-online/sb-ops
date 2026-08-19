package argo

import "time"

// Tunnel modes: temporary tunnels use trycloudflare quick tunnels (no
// account, address rotates on restart); token tunnels run a named tunnel
// owned by a Cloudflare account (stable domain).
const (
	ModeTemporary = "temporary"
	ModeToken     = "token"
)

// Tunnel runtime status values persisted by the supervisor.
const (
	StatusStopped  = "stopped"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusError    = "error"
)

type Tunnel struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Mode             string     `json:"mode"`
	FixedDomain      string     `json:"fixedDomain,omitempty"`
	PreferredAddress string     `json:"preferredAddress,omitempty"`
	ProtocolID       string     `json:"protocolId"`
	Token            string     `json:"token,omitempty"`
	TokenSet         bool       `json:"tokenSet,omitempty"`
	AssignedDomain   string     `json:"assignedDomain,omitempty"`
	Status           string     `json:"status"`
	LastError        string     `json:"lastError,omitempty"`
	StartedAt        *time.Time `json:"startedAt,omitempty"`
	Revision         int64      `json:"revision"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type TunnelWriteInput struct {
	ID               string
	Revision         int64
	Name             string
	Enabled          bool
	Mode             string
	FixedDomain      string
	PreferredAddress string
	ProtocolID       string
	Token            *string
}

// RuntimeUpdate carries supervisor-only state transitions (assigned domain,
// status, last error). It deliberately bypasses revision checks — runtime
// state is written by the panel process itself, not by concurrent admins.
type RuntimeUpdate struct {
	AssignedDomain string
	Status         string
	LastError      string
	StartedAt      *time.Time
}
