package observability

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DomesticConfig contains text/card robot webhook settings. Empty URLs disable
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
}

// DomesticTarget resolves current notification settings on every dispatch.
type DomesticTarget func(context.Context) (DomesticConfig, error)

// NewDomesticNotifier wires the Feishu/DingTalk/WeCom alert channels. Each
// channel prefers a card/markdown message; when there is nothing meaningful to
// show as a card (unparseable/empty payload) it falls back to the shared text
// summary. The target is resolved on every dispatch so config.yaml edits take
// effect without a restart.
func NewDomesticNotifier(target DomesticTarget) *domesticNotifier {
	return &domesticNotifier{target: target, client: &http.Client{Timeout: 10 * time.Second}}
}

func (n *domesticNotifier) Notify(ctx context.Context, value Notification) error {
	config, err := n.target(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	// 每个国内渠道独立发送；某一平台故障不能阻塞其它平台。
	if strings.TrimSpace(config.FeishuWebhookURL) != "" {
		if sendErr := n.postFeishu(ctx, config, value); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	if strings.TrimSpace(config.DingTalkWebhookURL) != "" {
		if sendErr := n.postDingTalk(ctx, config, value); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	if strings.TrimSpace(config.WeComWebhookURL) != "" {
		if sendErr := n.postWeCom(ctx, config, value); sendErr != nil && firstErr == nil {
			firstErr = sendErr
		}
	}
	return firstErr
}

func (n *domesticNotifier) postFeishu(ctx context.Context, config DomesticConfig, value Notification) error {
	endpoint := strings.TrimSpace(config.FeishuWebhookURL)
	if secret := strings.TrimSpace(config.FeishuSecret); secret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		endpoint = addQuery(endpoint, "timestamp", timestamp)
		endpoint = addQuery(endpoint, "sign", sign(secret, timestamp))
	}
	payload := feishuCardPayload(value)
	if payload == nil {
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": formatNotificationText(value)}}
	}
	return n.postJSON(ctx, endpoint, payload, "飞书")
}

func (n *domesticNotifier) postDingTalk(ctx context.Context, config DomesticConfig, value Notification) error {
	endpoint := strings.TrimSpace(config.DingTalkWebhookURL)
	if secret := strings.TrimSpace(config.DingTalkSecret); secret != "" {
		timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		endpoint = addQuery(endpoint, "timestamp", timestamp)
		endpoint = addQuery(endpoint, "sign", sign(secret, timestamp))
	}
	payload := dingtalkCardPayload(value)
	if payload == nil {
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": formatNotificationText(value)}}
	}
	return n.postJSON(ctx, endpoint, payload, "钉钉")
}

func (n *domesticNotifier) postWeCom(ctx context.Context, config DomesticConfig, value Notification) error {
	payload := wecomCardPayload(value)
	if payload == nil {
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": formatNotificationText(value)}}
	}
	return n.postJSON(ctx, strings.TrimSpace(config.WeComWebhookURL), payload, "企业微信")
}

func (n *domesticNotifier) postJSON(ctx context.Context, endpoint string, payload any, channel string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码%s通知失败: %w", channel, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建%s通知请求失败: %w", channel, sanitizeTransportError(err))
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("发送%s通知失败: %w", channel, sanitizeTransportError(err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("发送%s通知：异常状态码 %d", channel, response.StatusCode)
	}
	// 三家平台的业务失败（签名错、IP 白名单拒绝、机器人失效、频控等）大多
	// 仍回 HTTP 200，只看状态码会把拒绝当成功静默吞掉——曾因此连吞一周
	// 每日报告（面板全绿、群里无消息）。必须再校验响应体业务码：飞书
	// code/StatusCode、钉钉/企微 errcode。响应体非 JSON 时宽松放行，只拦截
	// 可确信的业务失败。
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return fmt.Errorf("读取%s通知响应失败: %w", channel, err)
	}
	var result struct {
		Code       int    `json:"code"`
		StatusCode int    `json:"StatusCode"`
		ErrCode    int    `json:"errcode"`
		Msg        string `json:"msg"`
		ErrMsg     string `json:"errmsg"`
	}
	if json.Unmarshal(responseBody, &result) != nil {
		return nil
	}
	code := result.Code
	if code == 0 {
		code = result.StatusCode
	}
	if code == 0 {
		code = result.ErrCode
	}
	if code != 0 {
		message := result.Msg
		if message == "" {
			message = result.ErrMsg
		}
		if message != "" {
			return fmt.Errorf("发送%s通知：平台业务错误 code=%d（%s）", channel, code, message)
		}
		return fmt.Errorf("发送%s通知：平台业务错误 code=%d", channel, code)
	}
	return nil
}

// alertSummary 只提取出站通知安全展示的字段；payload 含订阅/密钥等敏感字段时
// 不随卡片下发，仅用这四个展示字段。
type alertSummary struct {
	title     string // 非空表示报告类消息（title+message 形状），走报告卡片
	alertType string
	severity  string
	message   string
	status    string
}

func summarize(value Notification) alertSummary {
	var raw struct {
		Title    string `json:"title"`
		Type     string `json:"type"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Status   string `json:"status"`
	}
	// 解析失败按空载荷处理（此时各卡片 builder 回退纯文本）。
	_ = json.Unmarshal(value.Payload, &raw)
	return alertSummary{
		title:     strings.TrimSpace(raw.Title),
		alertType: strings.TrimSpace(raw.Type),
		severity:  strings.ToLower(strings.TrimSpace(raw.Severity)),
		message:   strings.TrimSpace(raw.Message),
		status:    strings.TrimSpace(raw.Status),
	}
}

// 出站卡片用的小型类型标签映射，与前端 util/labels.ts 的 alertTypeLabels 对齐。
var alertTypeLabels = map[string]string{
	"runtime_sync_failed":   "配置同步失败",
	"database_capacity":     "数据库容量预警",
	"aggregation_failed":    "流量聚合失败",
	"traffic_stalled":       "流量采集停滞",
	"protocol_probe_failed": "协议链路拨测失败",
	"certificate_expiring":  "证书即将到期",
	"vps_bandwidth_high":    "VPS 月流量用量过高",
	"vps_bandwidth_stalled": "VPS 月流量计量停滞",
	"upgrade_available":     "面板有新版本",
	"backup_failed":         "定时备份失败",
}

func cardTitle(severity string) (title, template string) {
	switch severity {
	case "critical":
		return "🔴 严重告警", "red"
	case "warning":
		return "🟡 警告", "yellow"
	default:
		return "🔔 通知", "blue"
	}
}

func severityText(severity string) string {
	switch severity {
	case "critical":
		return "严重"
	case "warning":
		return "警告"
	default:
		return "通知"
	}
}

func statusText(status string) string {
	switch strings.ToLower(status) {
	case "open":
		return "开放"
	case "resolved":
		return "已恢复"
	default:
		if status == "" {
			return "—"
		}
		return status
	}
}

// markdownBody 拼出卡片多选题：类型 / 消息 / 状态 / 时间，时间取本机当前
// 时间（dispatch 每 5s 一轮，与告警变更时间偏差在秒级）。
func markdownBody(s alertSummary) string {
	label := alertTypeLabels[s.alertType]
	if label == "" {
		label = s.alertType
	}
	if label == "" {
		label = "未知"
	}
	return fmt.Sprintf("**级别**：%s\n**类型**：%s\n**消息**：%s\n**状态**：%s\n**时间**：%s",
		severityText(s.severity), label, s.message, statusText(s.status), time.Now().Format("2006-01-02 15:04:05"))
}

// 三个卡片 builder 在载荷无可展示内容时返回 nil，由调用方回退纯文本。
// 载荷带 title（每日报告）时走报告形状：标题即卡片头，正文原样多行展示。

func feishuCardPayload(value Notification) map[string]any {
	s := summarize(value)
	if s.title != "" {
		return map[string]any{
			"msg_type": "interactive",
			"card": map[string]any{
				"config": map[string]any{"wide_screen_mode": true},
				"header": map[string]any{
					"title":    map[string]any{"tag": "plain_text", "content": s.title},
					"template": "blue",
				},
				"elements": []any{
					map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": s.message}},
				},
			},
		}
	}
	if s.severity == "" && s.message == "" {
		return nil
	}
	title, template := cardTitle(s.severity)
	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]any{"wide_screen_mode": true},
			"header": map[string]any{
				"title":    map[string]any{"tag": "plain_text", "content": title},
				"template": template,
			},
			"elements": []any{
				map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": markdownBody(s)}},
			},
		},
	}
}

func dingtalkCardPayload(value Notification) map[string]any {
	s := summarize(value)
	if s.title != "" {
		return map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": s.title, "text": s.message},
		}
	}
	if s.severity == "" && s.message == "" {
		return nil
	}
	title, _ := cardTitle(s.severity)
	return map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"title": title, "text": markdownBody(s)},
	}
}

func wecomCardPayload(value Notification) map[string]any {
	s := summarize(value)
	if s.title != "" {
		return map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"content": "**" + s.title + "**\n" + s.message},
		}
	}
	if s.severity == "" && s.message == "" {
		return nil
	}
	title, _ := cardTitle(s.severity)
	return map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": "# " + title + "\n" + markdownBody(s)},
	}
}

func sign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "\n" + secret))
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
