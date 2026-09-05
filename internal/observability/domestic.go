package observability

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DomesticConfig contains text-only robot webhook settings. Empty URLs disable
// their respective channels; secrets are optional unless the platform requires
// signature verification to be enabled on the robot.
type DomesticConfig struct {
	FeishuWebhookURL   string
	FeishuSecret       string
	DingTalkWebhookURL string
	DingTalkSecret     string
	WeComWebhookURL    string
}

type domesticNotifier struct {
	target DomesticTarget
	client *http.Client
	// base URLs are overridden in tests to route the exact platform endpoints.
	feishuBase, dingTalkBase, weComBase string
}

// DomesticTarget resolves current notification settings on every dispatch.
type DomesticTarget func(context.Context) (DomesticConfig, error)

// NewDomesticNotifier wires the Feishu/DingTalk/WeCom text alert channels. The
// target is resolved on every dispatch so config.yaml edits take effect without
// a restart.
func NewDomesticNotifier(target DomesticTarget) *domesticNotifier {
	return &domesticNotifier{
		target: target, client: &http.Client{Timeout: 10 * time.Second},
		feishuBase: "", dingTalkBase: "", weComBase: "",
	}
}

func (n *domesticNotifier) Notify(ctx context.Context, value Notification) error {
	config, err := n.target(ctx)
	if err != nil {
		return err
	}
	text := formatNotificationText(value)
	var firstErr error
	// 每个国内渠道独立发送；某一平台故障不能阻塞其它平台。
	if strings.TrimSpace(config.FeishuWebhookURL) != "" {
		if sendErr := n.postFeishu(ctx, config, text); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	if strings.TrimSpace(config.DingTalkWebhookURL) != "" {
		if sendErr := n.postDingTalk(ctx, config, text); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	if strings.TrimSpace(config.WeComWebhookURL) != "" {
		if sendErr := n.postWeCom(ctx, config, text); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	return firstErr
}

func (n *domesticNotifier) postFeishu(ctx context.Context, config DomesticConfig, text string) error {
	endpoint := strings.TrimSpace(config.FeishuWebhookURL)
	if secret := strings.TrimSpace(config.FeishuSecret); secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		endpoint = addQuery(endpoint, "timestamp", timestamp)
		endpoint = addQuery(endpoint, "sign", sign(secret, timestamp))
	}
	return n.postJSON(ctx, endpoint, map[string]any{
		"msg_type": "text", "content": map[string]string{"text": text},
	}, "飞书")
}

func (n *domesticNotifier) postDingTalk(ctx context.Context, config DomesticConfig, text string) error {
	endpoint := strings.TrimSpace(config.DingTalkWebhookURL)
	if secret := strings.TrimSpace(config.DingTalkSecret); secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		endpoint = addQuery(endpoint, "timestamp", timestamp)
		endpoint = addQuery(endpoint, "sign", sign(secret, timestamp))
	}
	return n.postJSON(ctx, endpoint, map[string]any{
		"msgtype": "text", "text": map[string]string{"content": text},
	}, "钉钉")
}

func (n *domesticNotifier) postWeCom(ctx context.Context, config DomesticConfig, text string) error {
	return n.postJSON(ctx, strings.TrimSpace(config.WeComWebhookURL), map[string]any{
		"msgtype": "text", "text": map[string]string{"content": text},
	}, "企业微信")
}

func (n *domesticNotifier) postJSON(ctx context.Context, endpoint string, payload any, channel string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码%s通知失败: %w", channel, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建%s通知请求失败: %w", channel, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("发送%s通知失败: %w", channel, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("发送%s通知：异常状态码 %d", channel, response.StatusCode)
	}
	return nil
}

func sign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func addQuery(raw, key, value string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
