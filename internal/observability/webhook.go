package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// WebhookConfig carries the externally configurable notification endpoints.
// Both are optional: an empty URL and empty Telegram credentials disable
// outbound delivery.
type WebhookConfig struct {
	URL      string // generic webhook endpoint (http/https)
	Secret   string // optional shared secret sent as the X-Webhook-Secret header
	BotToken string // Telegram bot token
	ChatID   string // Telegram chat identifier
}

// WebhookTarget resolves the current notification endpoints. The webhook
// notifier resolves them on every dispatch so settings changes take effect
// without a restart.
type WebhookTarget func(context.Context) (WebhookConfig, error)

type WebhookNotifier struct {
	target       WebhookTarget
	client       *http.Client
	telegramBase string
}

func NewWebhookNotifier(target WebhookTarget) *WebhookNotifier {
	return &WebhookNotifier{
		target:       target,
		client:       &http.Client{Timeout: 10 * time.Second},
		telegramBase: "https://api.telegram.org",
	}
}

func (n *WebhookNotifier) Notify(ctx context.Context, value Notification) error {
	config, err := n.target(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.URL) != "" {
		return n.postWebhook(ctx, config, value)
	}
	if strings.TrimSpace(config.BotToken) != "" && strings.TrimSpace(config.ChatID) != "" {
		return n.postTelegram(ctx, config, value)
	}
	return nil
}

func (n *WebhookNotifier) postWebhook(ctx context.Context, config WebhookConfig, value Notification) error {
	body, err := json.Marshal(map[string]any{"event": value.EventType, "alert": json.RawMessage(value.Payload)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(config.URL), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if secret := strings.TrimSpace(config.Secret); secret != "" {
		request.Header.Set("X-Webhook-Secret", secret)
	}
	return n.do(request)
}

func (n *WebhookNotifier) postTelegram(ctx context.Context, config WebhookConfig, value Notification) error {
	body, err := json.Marshal(map[string]any{"chat_id": strings.TrimSpace(config.ChatID), "text": formatNotificationText(value)})
	if err != nil {
		return err
	}
	endpoint := n.telegramBase + "/bot" + strings.TrimSpace(config.BotToken) + "/sendMessage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	return n.do(request)
}

func (n *WebhookNotifier) do(request *http.Request) error {
	response, err := n.client.Do(request)
	if err != nil {
		return fmt.Errorf("发送通知: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("发送通知：异常状态码 %d", response.StatusCode)
	}
	return nil
}

// formatNotificationText renders the alert payload as a compact single block
// suitable for a Telegram message. Payloads carrying a title (daily reports)
// render as title + body directly — no severity icon or status trailer.
func formatNotificationText(value Notification) string {
	var summary struct {
		Title    string `json:"title"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Status   string `json:"status"`
	}
	_ = json.Unmarshal(value.Payload, &summary)
	if title := strings.TrimSpace(summary.Title); title != "" {
		return title + "\n" + strings.TrimSpace(summary.Message)
	}
	icon := "🔔"
	switch summary.Severity {
	case "critical":
		icon = "🔴"
	case "warning":
		icon = "🟡"
	}
	line := strings.TrimSpace(summary.Message)
	if line == "" {
		line = "operation alert"
	}
	return fmt.Sprintf("%s %s\n%s（状态：%s）", icon, strings.ToUpper(summary.Severity), line, summary.Status)
}

// CompositeNotifier fans a notification out to several notifiers and reports
// the first error. Duplicate delivery is safe because the event bus dedupes by
// idempotency key and webhook receivers are expected to be idempotent.
type CompositeNotifier struct {
	notifiers []Notifier
}

func NewCompositeNotifier(notifiers ...Notifier) *CompositeNotifier {
	return &CompositeNotifier{notifiers: notifiers}
}

func (n *CompositeNotifier) Notify(ctx context.Context, value Notification) error {
	var firstErr error
	for _, notifier := range n.notifiers {
		if notifier == nil {
			continue
		}
		if err := notifier.Notify(ctx, value); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
