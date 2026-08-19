package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestWebhookNotifierPostsGenericWebhook(t *testing.T) {
	var mu sync.Mutex
	var receivedSecret string
	received := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedSecret = r.Header.Get("X-Webhook-Secret")
		_ = json.NewDecoder(r.Body).Decode(&received)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(func(context.Context) (WebhookConfig, error) {
		return WebhookConfig{URL: server.URL, Secret: "s3cret"}, nil
	})
	err := notifier.Notify(context.Background(), Notification{
		ID: "n1", EventType: "alert.open",
		Payload: json.RawMessage(`{"id":"a1","type":"runtime_sync_failed","severity":"critical","message":"sync failed","status":"open"}`),
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if receivedSecret != "s3cret" {
		t.Fatalf("X-Webhook-Secret=%q, want s3cret", receivedSecret)
	}
	if received["event"] != "alert.open" {
		t.Fatalf("event=%v", received["event"])
	}
	alert, ok := received["alert"].(map[string]any)
	if !ok || alert["severity"] != "critical" || alert["message"] != "sync failed" {
		t.Fatalf("alert=%v", received["alert"])
	}
}

func TestWebhookNotifierRetriesOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(func(context.Context) (WebhookConfig, error) {
		return WebhookConfig{URL: server.URL}, nil
	})
	err := notifier.Notify(context.Background(), Notification{ID: "n1", EventType: "alert.open", Payload: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err=%v, want non-2xx error", err)
	}
}

func TestWebhookNotifierPostsTelegram(t *testing.T) {
	var mu sync.Mutex
	var receivedPath string
	received := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&received)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(func(context.Context) (WebhookConfig, error) {
		return WebhookConfig{BotToken: "123:TOKEN", ChatID: "-100123"}, nil
	})
	notifier.telegramBase = server.URL

	err := notifier.Notify(context.Background(), Notification{
		ID: "n2", EventType: "alert.resolved",
		Payload: json.RawMessage(`{"id":"a1","type":"runtime_sync_failed","severity":"warning","message":"sync failed","status":"resolved"}`),
	})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if receivedPath != "/bot123:TOKEN/sendMessage" {
		t.Fatalf("path=%q", receivedPath)
	}
	if received["chat_id"] != "-100123" {
		t.Fatalf("chat_id=%v", received["chat_id"])
	}
	text, _ := received["text"].(string)
	if !strings.Contains(text, "sync failed") || !strings.Contains(text, "WARNING") {
		t.Fatalf("text=%q", text)
	}
}
