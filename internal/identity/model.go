package identity

import "time"

type Account struct {
	ID           string
	Username     string
	PasswordHash []byte
	Enabled      bool
	Revision     int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID        string
	AccountID string
	TokenHash []byte
	ClientIP  string
	UserAgent string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}
