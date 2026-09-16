package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/config"
)

func TestUnavailableProfileCarriesReason(t *testing.T) {
	profile := string(unavailableProfile("订阅流量已耗尽"))
	for _, want := range []string{"订阅流量已耗尽", "type: socks5", "proxy-groups", "rules"} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	if strings.Count(profile, "订阅流量已耗尽") != 2 {
		t.Fatal("reason must appear in both the node name and the group reference")
	}
}

func TestSubscriptionBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("uses configured SubscriptionURLBase", func(t *testing.T) {
		h := &handler{
			Config: func() config.RuntimeConfig {
				cfg := config.DefaultRuntimeConfig()
				// 加载期已归一化（去空白与末尾斜杠），handler 直接透传。
				cfg.Deployment.SubscriptionURLBase = "https://sub.example.com"
				return cfg
			},
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users/123/devices", nil)
		c.Request.Host = "internal.example.com"

		got := h.subscriptionBaseURL(c)
		if got != "https://sub.example.com" {
			t.Fatalf("expected https://sub.example.com, got %q", got)
		}
	})

	t.Run("ignores spoofable forwarded host when unconfigured", func(t *testing.T) {
		h := &handler{
			Config: func() config.RuntimeConfig {
				return config.DefaultRuntimeConfig()
			},
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users/123/devices", nil)
		c.Request.Host = "panel.example.com"
		c.Request.Header.Set("X-Forwarded-Proto", "https")
		// X-Forwarded-Host 不受可信代理机制保护、直连即可伪造：采信它会让
		// 展示的订阅链接（含 token）指向攻击者主机。只认 Host 头。
		c.Request.Header.Set("X-Forwarded-Host", "attacker.example.com")

		got := h.subscriptionBaseURL(c)
		if got != "https://panel.example.com" {
			t.Fatalf("expected https://panel.example.com, got %q", got)
		}
	})

	t.Run("falls back to request host when no headers", func(t *testing.T) {
		h := &handler{
			Config: func() config.RuntimeConfig {
				return config.DefaultRuntimeConfig()
			},
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/users/123/devices", nil)
		c.Request.Host = "10.0.0.1:8080"

		got := h.subscriptionBaseURL(c)
		if got != "http://10.0.0.1:8080" {
			t.Fatalf("expected http://10.0.0.1:8080, got %q", got)
		}
	})
}
