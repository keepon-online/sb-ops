package upgrade

// Phase 标记一次升级流程当前所处的阶段。idle 表示空闲，failed 表示上次
// 尝试已终止（可重新发起）；Active 阶段期间拒绝发起新的检查与升级。
type Phase string

const (
	PhaseIdle        Phase = "idle"
	PhaseChecking    Phase = "checking"
	PhaseDownloading Phase = "downloading"
	PhaseVerifying   Phase = "verifying"
	PhaseApplying    Phase = "applying"
	PhaseRestarting  Phase = "restarting"
	PhaseFailed      Phase = "failed"
)

// Active 报告该阶段是否处于一次进行中的升级（此时 Check/Start 都应让位）。
func (p Phase) Active() bool {
	switch p {
	case PhaseDownloading, PhaseVerifying, PhaseApplying, PhaseRestarting:
		return true
	}
	return false
}

// Release 是一次成功检查后缓存的最新发行版信息。
type Release struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	PublishedAt int64  `json:"publishedAt"` // unix 秒，0 表示未知
	Upgradable  bool   `json:"upgradable"`
}

// Status 是升级服务的完整对外快照。升级源 token 永不包含在内。
type Status struct {
	Configured     bool     `json:"configured"`
	Repo           string   `json:"repo"`
	CurrentVersion string   `json:"currentVersion"`
	Phase          Phase    `json:"phase"`
	Latest         *Release `json:"latest,omitempty"`
	Error          string   `json:"error,omitempty"`
	UpdatedAt      int64    `json:"updatedAt"` // unix 秒，0 表示尚未发生任何状态变化
}
