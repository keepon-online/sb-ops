package audit

import "time"

// Record 是一条待落盘的审计记录（写入侧）；DetailsJSON 由调用方拼好。
type Record struct {
	ID           string
	ActorID      *string
	Action       string
	ResourceType string
	ResourceID   string
	Result       string
	RequestID    string
	ClientIP     string
	DetailsJSON  string
	CreatedAt    time.Time
}

type Query struct {
	Cursor                                    string
	Limit                                     int
	Actor, Resource, Action, Result, From, To string
}

type Entry struct {
	ID           string    `json:"id"`
	ActorID      *string   `json:"actorId,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Result       string    `json:"result"`
	RequestID    string    `json:"requestId"`
	ClientIP     string    `json:"clientIp"`
	Details      any       `json:"details"`
	CreatedAt    time.Time `json:"createdAt"`
}

type Page struct {
	Items      []Entry `json:"items"`
	NextCursor string  `json:"nextCursor,omitempty"`
	Total      int     `json:"total"`
}
