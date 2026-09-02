package singlevps

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	deploymentcompiler "github.com/keepon-online/sb-ops/internal/deployment/compiler"
	runtimebox "github.com/keepon-online/sb-ops/internal/runtime/singbox"
	"github.com/keepon-online/sb-ops/internal/service/common"
	"github.com/keepon-online/sb-ops/internal/service/secret"
	servicesingbox "github.com/keepon-online/sb-ops/internal/service/singbox"
)

type ApplyConfig struct {
	CertificateDir      string
	PublicAddress       string
	TLSCertPath         string
	TLSKeyPath          string
	CacheFilePath       string
	AllowPrivateAddress bool
}

type SyncResult struct {
	ConfigDigest  string
	ArtifactCount int
	AppliedAt     time.Time
}

type RuntimeProbe struct {
	ConfigStatus string `json:"configStatus"`
	KernelStatus string `json:"kernelStatus"`
	Reason       string `json:"reason,omitempty"`
}

type RuntimeStatus struct {
	RuntimeProbe
	ConfigDigest    string     `json:"configDigest,omitempty"`
	LastAppliedAt   *time.Time `json:"lastAppliedAt,omitempty"`
	LastFailedAt    *time.Time `json:"lastFailedAt,omitempty"`
	FailureReason   string     `json:"failureReason,omitempty"`
	Version         string     `json:"version,omitempty"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	RuntimeMode     string     `json:"runtimeMode"`
	CanCheck        bool       `json:"canCheck"`
	CanRestart      bool       `json:"canRestart"`
	CanObserveConns bool       `json:"canObserveConnections"`
}

type CheckResult struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

type SyncService struct {
	db      *sql.DB
	apply   ApplyConfig
	runtime *runtimebox.Manager
	now     func() time.Time
	mu      sync.Mutex
	// Optional probe overrides wired by tests; nil falls back to the real
	// implementations in protocol_status.go.
	ListenSnapshot  func(ctx context.Context) (ListenSnapshot, error)
	PortHopSnapshot func(ctx context.Context) (string, error)
	KernelState     func() runtimebox.Status
}

func NewSyncService(db *sql.DB, apply ApplyConfig, logSinks ...runtimebox.LogSink) *SyncService {
	return &SyncService{db: db, apply: apply, runtime: runtimebox.New(logSinks...), now: func() time.Time { return time.Now().UTC() }}
}

func (s *SyncService) RuntimeStatus(ctx context.Context) RuntimeStatus {
	core := s.runtime.Status()
	canObserveConns := false
	if _, _, err := s.clashAPI(ctx); err == nil {
		canObserveConns = true
	}
	status := RuntimeStatus{
		RuntimeProbe:    s.Probe(ctx),
		Version:         core.Version,
		StartedAt:       core.StartedAt,
		RuntimeMode:     runtimebox.Mode,
		CanCheck:        true,
		CanRestart:      core.State == "running",
		CanObserveConns: canObserveConns,
	}
	var applied, failed common.Time
	if err := s.db.QueryRowContext(ctx, `SELECT config_digest,last_applied_at,last_failed_at,failure_reason FROM runtime_state WHERE id=1`).Scan(&status.ConfigDigest, &applied, &failed, &status.FailureReason); err == nil {
		status.LastAppliedAt = applied.Ptr()
		status.LastFailedAt = failed.Ptr()
	}
	if core.Reason != "" && status.FailureReason == "" {
		status.FailureReason = core.Reason
	}
	return status
}

func (s *SyncService) Check(ctx context.Context) (CheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, compiled, err := s.compile(ctx)
	if err != nil {
		return CheckResult{Valid: false, Message: diagnosticReason(err)}, err
	}
	if err := s.runtime.Check(ctx, compiled.ServerConfig); err != nil {
		return CheckResult{Valid: false, Message: diagnosticReason(err)}, err
	}
	return CheckResult{Valid: true, Message: "当前托管配置通过嵌入式 sing-box 检查"}, nil
}

func (s *SyncService) Start(ctx context.Context) error {
	if !s.deploymentConfigured() {
		return nil
	}
	_, err := s.Sync(ctx)
	return err
}

func (s *SyncService) Reconcile(ctx context.Context) (SyncResult, error) {
	return s.Sync(ctx)
}

func (s *SyncService) Restart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime.Restart(ctx)
}

func (s *SyncService) Probe(ctx context.Context) RuntimeProbe {
	probe := RuntimeProbe{ConfigStatus: "not_configured", KernelStatus: "not_checked"}
	if !s.deploymentConfigured() {
		probe.Reason = "尚未配置公网地址。请在 config.yaml 的 deployment.public_address 填写公网 IPv4 或域名，然后重启或重新同步。"
		return probe
	}
	probe.ConfigStatus = "pending"
	core := s.runtime.Status()
	if core.State == "running" {
		probe.ConfigStatus = "applied"
		probe.KernelStatus = "running"
		return probe
	}
	if core.Reason != "" {
		probe.KernelStatus = "unhealthy"
		probe.Reason = core.Reason
	}
	return probe
}

func (s *SyncService) deploymentConfigured() bool {
	return strings.TrimSpace(s.apply.PublicAddress) != ""
}

func diagnosticReason(err error) string {
	if errors.Is(err, deploymentcompiler.ErrPublicAddressNotConfigured) {
		return "尚未配置公网地址。请在系统设置中填写公网 IPv4 或域名，然后重新同步。"
	}
	return truncateDiagnostic(err.Error())
}

func truncateDiagnostic(value string) string {
	const limit = 800
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func (s *SyncService) Sync(ctx context.Context) (SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input, compiled, err := s.compile(ctx)
	if err == nil {
		result := SyncResult{ArtifactCount: len(compiled.Subscriptions), AppliedAt: s.now()}
		digest := sha256.Sum256(compiled.ServerConfig)
		result.ConfigDigest = hex.EncodeToString(digest[:])
		if err = s.runtime.Apply(ctx, compiled.ServerConfig); err == nil {
			err = saveApplied(ctx, s.db, result, compiled.Subscriptions)
		}
		if err == nil {
			if hopErr := reconcilePortHop(ctx, input.Protocols); hopErr != nil {
				slog.Error("端口跳跃规则应用失败", "error", hopErr)
			}
			return result, nil
		}
	}
	if recordErr := s.recordFailure(ctx, err); recordErr != nil {
		return SyncResult{}, fmt.Errorf("同步嵌入式 sing-box: %w；记录失败: %v", err, recordErr)
	}
	return SyncResult{}, err
}

func (s *SyncService) compile(ctx context.Context) (deploymentcompiler.Input, deploymentcompiler.Result, error) {
	input, err := deploymentcompiler.LoadInput(ctx, s.db, deploymentcompiler.Deployment{
		PublicAddress:       s.apply.PublicAddress,
		TLSCertPath:         s.apply.TLSCertPath,
		TLSKeyPath:          s.apply.TLSKeyPath,
		CacheFilePath:       s.apply.CacheFilePath,
		AllowPrivateAddress: s.apply.AllowPrivateAddress,
	}, s.apply.CertificateDir)
	if err != nil {
		return deploymentcompiler.Input{}, deploymentcompiler.Result{}, err
	}
	compiled, err := deploymentcompiler.Compile(input)
	if err != nil {
		return deploymentcompiler.Input{}, deploymentcompiler.Result{}, err
	}
	return input, compiled, nil
}

// clashAPI resolves the local Clash API base URL and secret from the
// panel-managed sing-box configuration — the single source of truth for the
// embedded core.
func (s *SyncService) clashAPI(ctx context.Context) (endpoint, clashSecret string, err error) {
	singBox := servicesingbox.NewService(servicesingbox.NewStore(s.db, secret.NewStore(s.db)))
	endpoint, clashSecret, enabled, err := singBox.ClashAPIEndpoint(ctx)
	if err != nil {
		return "", "", err
	}
	if !enabled {
		return "", "", errors.New("未启用 Clash API")
	}
	endpoint, err = clashEndpoint(endpoint)
	if err != nil {
		return "", "", err
	}
	return endpoint, clashSecret, nil
}

func (s *SyncService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime.Close()
}

func saveApplied(ctx context.Context, db *sql.DB, result SyncResult, subscriptions []deploymentcompiler.Subscription) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM runtime_artifacts`); err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		digest := sha256.Sum256(subscription.Content)
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_artifacts(user_id,device_id,format,content,digest,updated_at) VALUES(?,?,?,?,?,?)`, subscription.UserID, subscription.DeviceID, "clash.yaml", subscription.Content, hex.EncodeToString(digest[:]), result.AppliedAt.Unix()); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO runtime_state(id,config_digest,last_applied_at,last_failed_at,failure_reason,artifact_count) VALUES(1,?,?,NULL,'',?)
ON CONFLICT(id) DO UPDATE SET config_digest=excluded.config_digest,last_applied_at=excluded.last_applied_at,last_failed_at=NULL,failure_reason='',artifact_count=excluded.artifact_count`, result.ConfigDigest, result.AppliedAt.Unix(), result.ArtifactCount)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SyncService) recordFailure(ctx context.Context, cause error) error {
	if cause == nil {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO runtime_state(id,config_digest,last_applied_at,last_failed_at,failure_reason,artifact_count) VALUES(1,'',NULL,?,?,0)
ON CONFLICT(id) DO UPDATE SET last_failed_at=excluded.last_failed_at,failure_reason=excluded.failure_reason`, s.now().Unix(), cause.Error())
	return err
}
