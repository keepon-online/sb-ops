package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	serviceshare "github.com/keepon-online/sb-ops/internal/service/share"
)

func (h *handler) downloadSubscription(c *gin.Context) {
	ifNoneMatch := c.GetHeader("If-None-Match")
	artifact, err := h.Share.ArtifactByToken(c.Request.Context(), c.Param("token"), c.Param("format"), c.Request.UserAgent(), c.ClientIP(), ifNoneMatch)
	if err != nil {
		switch {
		case errors.Is(err, serviceshare.ErrUserDisabled), errors.Is(err, serviceshare.ErrUserExpired), errors.Is(err, serviceshare.ErrQuotaExhausted), errors.Is(err, serviceshare.ErrDeviceDisabled):
			// Clash 系客户端更新订阅失败时只显示状态码、不展示响应体,用户看不到
			// 不可用原因。改为返回 200 + 占位配置,把原因写进节点名(机场惯例),
			// 客户端代理列表即可直接看到中文原因;占位节点指向不可达地址,不会被误用。
			c.Header("Cache-Control", "no-store")
			c.Data(http.StatusOK, "text/yaml; charset=utf-8", unavailableProfile(unavailableReasonFor(err)))
		case errors.Is(err, serviceshare.ErrTokenNotFound), errors.Is(err, serviceshare.ErrRuntimeNotReady), errors.Is(err, serviceshare.ErrArtifactNotFound):
			c.Data(http.StatusNotFound, "text/plain; charset=utf-8", []byte("订阅不存在"))
		default:
			c.Data(http.StatusInternalServerError, "text/plain; charset=utf-8", []byte("内部服务器错误"))
		}
		return
	}
	etag := `"` + artifact.Digest + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-cache")
	if serviceshare.CleanETag(ifNoneMatch) == artifact.Digest {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("Subscription-Userinfo", artifact.UserInfo())
	c.Data(http.StatusOK, artifact.ContentType, artifact.Content)
}

// unavailableReasonFor maps the share-service sentinel errors to fixed
// client-facing text. The raw error string must never reach the node name:
// today the sentinels carry fixed Chinese messages, but any future wrapping
// that embeds user data would otherwise inject into the YAML profile.
func unavailableReasonFor(err error) string {
	switch {
	case errors.Is(err, serviceshare.ErrUserDisabled):
		return "订阅已停用"
	case errors.Is(err, serviceshare.ErrDeviceDisabled):
		return "设备已停用"
	case errors.Is(err, serviceshare.ErrUserExpired):
		return "订阅已过期"
	case errors.Is(err, serviceshare.ErrQuotaExhausted):
		return "订阅流量已耗尽"
	default:
		return "订阅不可用"
	}
}

// unavailableProfile renders a valid but non-functional Clash config whose only
// node name carries the unavailable reason, so clients that ignore error
// response bodies (Clash Verge 等) still surface it in the proxy list.
func unavailableProfile(reason string) []byte {
	// Defense in depth: the node name is quoted YAML — strip quote-breaking
	// and line-breaking characters even though current reasons are fixed text.
	reason = strings.NewReplacer(`"`, "", "\n", " ", "\r", " ").Replace(reason)
	return []byte(fmt.Sprintf(`port: 7890
socks-port: 7891
allow-lan: false
mode: rule
log-level: info
proxies:
  - name: "❌ %s"
    type: socks5
    server: 127.0.0.1
    port: 1
proxy-groups:
  - name: 节点选择
    type: select
    proxies:
      - "❌ %s"
rules:
  - MATCH,节点选择
`, reason, reason))
}

// subscriptionBaseURL 返回对外分发订阅链接所使用的根地址。
//  1. 若配置了 deployment.subscription_url_base，以此为单一事实来源（加载期已
//     归一化：绝对 http(s) 地址、无末尾斜杠）；
//  2. 未配置时，从当前 HTTP 请求头动态提取（支持反向代理 X-Forwarded-Proto 与 X-Forwarded-Host）。
func (h *handler) subscriptionBaseURL(c *gin.Context) string {
	if base := h.Config().Deployment.SubscriptionURLBase; base != "" {
		return base
	}
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.GetHeader("X-Forwarded-Host")
	if host == "" {
		host = c.Request.Host
	}
	return scheme + "://" + host
}
