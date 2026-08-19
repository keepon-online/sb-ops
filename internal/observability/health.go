package observability

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

type ReadinessCheck struct {
	Status        string     `json:"status"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
}

type Readiness struct {
	Status string                    `json:"status"`
	Checks map[string]ReadinessCheck `json:"checks"`
}

type operationHealth struct {
	lastSuccess time.Time
	failed      bool
}

type HealthState struct {
	mu          sync.RWMutex
	now         func() time.Time
	background  bool
	aggregation operationHealth
	collection  operationHealth
	alerts      operationHealth
}

func NewHealthState(now func() time.Time) *HealthState {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &HealthState{now: now}
}

func (s *HealthState) MarkBackgroundRunning(running bool) {
	s.mu.Lock()
	s.background = running
	s.mu.Unlock()
}

func (s *HealthState) RecordAggregation(err error) {
	s.record(&s.aggregation, err)
}

func (s *HealthState) RecordTrafficCollection(err error) {
	s.record(&s.collection, err)
}

func (s *HealthState) RecordAlertEvaluation(err error) {
	s.record(&s.alerts, err)
}

func (s *HealthState) record(target *operationHealth, err error) {
	s.mu.Lock()
	target.failed = err != nil
	if err == nil {
		target.lastSuccess = s.now()
	}
	s.mu.Unlock()
}

func (s *HealthState) Snapshot(ctx context.Context, db *sql.DB, expectedSchema int) Readiness {
	now := s.now()
	checks := map[string]ReadinessCheck{}
	checks["database"] = readinessCheck(db.PingContext(ctx) == nil, nil)

	var schemaVersion int
	schemaErr := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_versions`).Scan(&schemaVersion)
	checks["schema"] = readinessCheck(schemaErr == nil && schemaVersion == expectedSchema, nil)

	s.mu.RLock()
	background := s.background
	aggregation, collection, alerts := s.aggregation, s.collection, s.alerts
	s.mu.RUnlock()
	checks["background"] = readinessCheck(background, nil)
	checks["trafficAggregation"] = operationReadiness(aggregation, now)
	checks["trafficCollection"] = operationReadiness(collection, now)
	checks["alertEvaluation"] = operationReadiness(alerts, now)

	status := "ready"
	for _, check := range checks {
		if check.Status != "ok" {
			status = "not_ready"
			break
		}
	}
	return Readiness{Status: status, Checks: checks}
}

func operationReadiness(value operationHealth, now time.Time) ReadinessCheck {
	ok := !value.failed && !value.lastSuccess.IsZero() && now.Sub(value.lastSuccess) <= 3*time.Minute
	var lastSuccess *time.Time
	if !value.lastSuccess.IsZero() {
		copy := value.lastSuccess
		lastSuccess = &copy
	}
	return readinessCheck(ok, lastSuccess)
}

func readinessCheck(ok bool, lastSuccess *time.Time) ReadinessCheck {
	status := "failed"
	if ok {
		status = "ok"
	}
	return ReadinessCheck{Status: status, LastSuccessAt: lastSuccess}
}
