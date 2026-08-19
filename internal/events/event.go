package events

import (
	"encoding/json"
	"time"
)

type Event struct {
	ID             int64           `json:"id"`
	Type           string          `json:"type"`
	Scope          string          `json:"scope"`
	ResourceID     string          `json:"resourceId,omitempty"`
	Permission     string          `json:"permission,omitempty"`
	IdempotencyKey string          `json:"-"`
	Payload        json.RawMessage `json:"payload"`
	CreatedAt      time.Time       `json:"createdAt"`
}

type Input struct {
	Type, Scope, ResourceID, Permission string
	IdempotencyKey                      string
	Payload                             any
}
