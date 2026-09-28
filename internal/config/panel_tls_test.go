package config

import (
	"strings"
	"testing"
)

func TestPanelTLSListenValidation(t *testing.T) {
	base := func(mutate func(cfg *RuntimeConfig)) RuntimeConfig {
		cfg := DefaultRuntimeConfig()
		cfg.Server.Listen = "127.0.0.1:8030"
		cfg.Deployment.PanelDomain = "panel.example.com"
		cfg.Acme.Email = "admin@example.com"
		cfg.Acme.APIToken = "token"
		mutate(&cfg)
		return NormalizeRuntimeConfig(cfg)
	}

	cases := []struct {
		name   string
		mutate func(cfg *RuntimeConfig)
		want   string
	}{
		{"tls+redirect 齐备即合法", func(cfg *RuntimeConfig) {
			cfg.Server.TLSListen = ":443"
			cfg.Server.RedirectListen = ":80"
		}, ""},
		{"不启用即无前置", func(cfg *RuntimeConfig) {}, ""},
		{"缺 panel_domain 拒绝", func(cfg *RuntimeConfig) {
			cfg.Server.TLSListen = ":443"
			cfg.Deployment.PanelDomain = ""
		}, "deployment.panel_domain"},
		{"缺 acme 凭据拒绝", func(cfg *RuntimeConfig) {
			cfg.Server.TLSListen = ":443"
			cfg.Acme.APIToken = ""
		}, "acme"},
		{"redirect 无 tls 拒绝", func(cfg *RuntimeConfig) {
			cfg.Server.RedirectListen = ":80"
		}, "redirect_listen 仅在"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRuntimeConfig(base(tc.mutate))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("不期望错误: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("期望校验错误")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 %q 未包含 %q", err.Error(), tc.want)
			}
		})
	}
}

func TestPanelTLSListenChangeRequiresRestart(t *testing.T) {
	before := DefaultRuntimeConfig()
	after := DefaultRuntimeConfig()
	after.Server.TLSListen = ":443"
	summary := ClassifyRuntimeConfigChanges(before, after)
	if len(summary.RestartRequired) == 0 {
		t.Fatal("tls_listen 变更应标记 RestartRequired")
	}
}
