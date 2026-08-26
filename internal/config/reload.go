package config

type ReloadSummary struct {
	Immediate       []string
	NextRequest     []string
	RestartRequired []string
}

func ClassifyRuntimeConfigChanges(previous, next RuntimeConfig) ReloadSummary {
	summary := ReloadSummary{}

	if previous.Panel.SessionTTL != next.Panel.SessionTTL {
		// The TTL is fixed when the identity service is constructed, so a
		// change only takes effect after a restart.
		summary.RestartRequired = append(summary.RestartRequired, "panel.session_ttl")
	}
	if previous.Development.FrontendDevProxy != next.Development.FrontendDevProxy {
		// The asset backend (embedded vs dev proxy) is chosen once at startup.
		summary.RestartRequired = append(summary.RestartRequired, "development.frontend_dev_proxy")
	}
	if previous.Server.Listen != next.Server.Listen {
		summary.RestartRequired = append(summary.RestartRequired, "server.listen")
	}
	if previous.Server.DBPath != next.Server.DBPath {
		summary.RestartRequired = append(summary.RestartRequired, "server.db_path")
	}
	if previous.Server.DataDir != next.Server.DataDir {
		summary.RestartRequired = append(summary.RestartRequired, "server.data_dir")
	}
	if previous.Server.ReadHeaderTimeout != next.Server.ReadHeaderTimeout ||
		previous.Server.ReadTimeout != next.Server.ReadTimeout ||
		previous.Server.IdleTimeout != next.Server.IdleTimeout ||
		previous.Server.MaxHeaderBytes != next.Server.MaxHeaderBytes {
		summary.RestartRequired = append(summary.RestartRequired, "server.http_timeouts")
	}
	if previous.Logging != next.Logging {
		// The logger (sinks, level, rotation) is built once at startup.
		summary.RestartRequired = append(summary.RestartRequired, "logging")
	}
	if previous.Traffic.Interval != next.Traffic.Interval {
		// The collector is constructed with its interval at startup.
		summary.RestartRequired = append(summary.RestartRequired, "traffic.interval")
	}
	if previous.Deployment.PublicAddress != next.Deployment.PublicAddress {
		summary.RestartRequired = append(summary.RestartRequired, "deployment.public_address")
	}
	if previous.Deployment.TLSCertPath != next.Deployment.TLSCertPath {
		summary.RestartRequired = append(summary.RestartRequired, "deployment.tls_cert_path")
	}
	if previous.Deployment.TLSKeyPath != next.Deployment.TLSKeyPath {
		summary.RestartRequired = append(summary.RestartRequired, "deployment.tls_key_path")
	}
	if previous.Traffic.RetentionDays != next.Traffic.RetentionDays {
		summary.Immediate = append(summary.Immediate, "traffic.retention_days")
	}
	if previous.Notifications != next.Notifications {
		summary.Immediate = append(summary.Immediate, "notifications")
	}
	if previous.Updates != next.Updates {
		// The upgrade service reads the repo/token live on every operation.
		summary.Immediate = append(summary.Immediate, "updates")
	}
	return summary
}
