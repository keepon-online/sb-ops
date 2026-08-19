package traffic

import "time"

type Sample struct {
	UserID     string `json:"userId,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	ProtocolID string `json:"protocolId,omitempty"`
	UpBytes    int64  `json:"upBytes"`
	DownBytes  int64  `json:"downBytes"`
}

type Batch struct {
	ID          string    `json:"id"`
	BootID      string    `json:"bootId"`
	Sequence    int64     `json:"sequence"`
	CollectedAt time.Time `json:"collectedAt"`
	Samples     []Sample  `json:"samples"`
}

type IngestResult struct {
	AcceptedBatchID string   `json:"acceptedBatchId"`
	LastSequence    int64    `json:"lastSequence"`
	ChangedUsers    []string `json:"changedUsers"`
	Duplicate       bool     `json:"duplicate"`
}

type Filter struct {
	From, To           time.Time
	UserID, ProtocolID string
	Interval           string
}

type Point struct {
	Bucket     time.Time `json:"bucket"`
	UpBytes    int64     `json:"upBytes"`
	DownBytes  int64     `json:"downBytes"`
	TotalBytes int64     `json:"totalBytes"`
}

type Summary struct {
	UpBytes    int64 `json:"upBytes"`
	DownBytes  int64 `json:"downBytes"`
	TotalBytes int64 `json:"totalBytes"`
	Samples    int64 `json:"samples"`
}

type Ranking struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UpBytes    int64  `json:"upBytes"`
	DownBytes  int64  `json:"downBytes"`
	TotalBytes int64  `json:"totalBytes"`
}

// ActiveUserPoint is the count of distinct users with traffic in one bucket.
type ActiveUserPoint struct {
	Bucket time.Time `json:"bucket"`
	Users  int64     `json:"users"`
}

// ActiveUsersResult carries the per-bucket active-user trend plus the distinct
// user count over the whole range (per-bucket counts do not sum to it).
type ActiveUsersResult struct {
	Items      []ActiveUserPoint `json:"items"`
	TotalUsers int64             `json:"totalUsers"`
}

// ProtocolShare is one protocol's share of inbound-attributed traffic.
type ProtocolShare struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UpBytes    int64  `json:"upBytes"`
	DownBytes  int64  `json:"downBytes"`
	TotalBytes int64  `json:"totalBytes"`
}
