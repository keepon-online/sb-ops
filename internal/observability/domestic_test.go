package observability

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomesticNotifierPostsAllConfiguredChannels(t *testing.T) {
	requests := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		body["path"] = r.URL.Path
		body["query"] = r.URL.Query()
		requests <- body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{
			FeishuWebhookURL:   server.URL + "/feishu",
			FeishuSecret:       "feishu-secret",
			DingTalkWebhookURL: server.URL + "/dingtalk?access_token=abc",
			DingTalkSecret:     "ding-secret",
			WeComWebhookURL:    server.URL + "/wecom",
		}, nil
	})
	if err := notifier.Notify(context.Background(), Notification{EventType: "alert.open", Payload: json.RawMessage(`{"severity":"warning","message":"测试告警","status":"open"}`)}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		body := <-requests
		path, _ := body["path"].(string)
		seen[path] = true
		switch path {
		case "/feishu":
			card, ok := body["card"].(map[string]any)
			if !ok || body["msg_type"] != "interactive" {
				t.Fatalf("feishu 应为 interactive 卡片，got msg_type=%v", body["msg_type"])
			}
			header, _ := card["header"].(map[string]any)
			// warning 级别对应黄色模板。
			if header["template"] != "yellow" {
				t.Fatalf("feishu template=%v, want yellow", header["template"])
			}
			if _, ok := card["elements"].([]any); !ok {
				t.Fatalf("feishu elements 缺失: %v", card["elements"])
			}
		default:
			body2, ok := body["markdown"].(map[string]any)
			if !ok || body["msgtype"] != "markdown" {
				t.Fatalf("%s 应为 markdown，got msgtype=%v", path, body["msgtype"])
			}
			var text string
			if path == "/dingtalk" {
				text, _ = body2["text"].(string)
			} else {
				text, _ = body2["content"].(string)
			}
			for _, keyword := range []string{"测试告警", "状态", "时间"} {
				if !strings.Contains(text, keyword) {
					t.Fatalf("%s 卡片缺少 %q: %q", path, keyword, text)
				}
			}
		}
	}
	for _, path := range []string{"/feishu", "/dingtalk", "/wecom"} {
		if !seen[path] {
			t.Fatalf("channel %s was not called", path)
		}
	}
}

func TestDomesticNotifierSignsFeishuAndDingTalk(t *testing.T) {
	seen := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{FeishuWebhookURL: server.URL + "/feishu", FeishuSecret: "secret"}, nil
	})
	if err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	request := <-seen
	timestamp := request.URL.Query().Get("timestamp")
	if timestamp == "" || request.URL.Query().Get("sign") == "" {
		t.Fatalf("missing feishu signature: %s", request.URL)
	}
	if request.URL.Query().Get("sign") != hmacSign("secret", timestamp) {
		t.Fatal("invalid feishu signature")
	}

	notifier = NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{DingTalkWebhookURL: server.URL + "/ding?access_token=x", DingTalkSecret: "secret"}, nil
	})
	if err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	request = <-seen
	query := request.URL.Query()
	if query.Get("access_token") != "x" || query.Get("timestamp") == "" || query.Get("sign") == "" {
		t.Fatalf("invalid dingtalk query: %s", request.URL)
	}
	if query.Get("sign") != hmacSign("secret", query.Get("timestamp")) {
		t.Fatal("invalid dingtalk signature")
	}
}

func TestDomesticNotifierFallsBackToTextOnEmptyPayload(t *testing.T) {
	body := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		body <- decoded
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{FeishuWebhookURL: server.URL + "/feishu", DingTalkWebhookURL: server.URL + "/ding", WeComWebhookURL: server.URL + "/wecom"}, nil
	})
	// 空 payload：无可展示内容，三渠道都应降级为纯文本而非空卡片。
	if err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	for i := 0; i < 3; i++ {
		payload := <-body
		if payload["msg_type"] == "interactive" || payload["msgtype"] == "markdown" {
			t.Fatalf("空载荷不应发送卡片: %v", payload)
		}
	}
}

func TestDomesticNotifierContinuesAfterChannelFailure(t *testing.T) {
	called := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- r.URL.Path
		if r.URL.Path == "/feishu" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{FeishuWebhookURL: server.URL + "/feishu", WeComWebhookURL: server.URL + "/wecom"}, nil
	})
	if err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)}); err == nil || !strings.Contains(err.Error(), "飞书") {
		t.Fatalf("err=%v", err)
	}
	if <-called != "/feishu" || <-called != "/wecom" {
		t.Fatal("second channel was not attempted")
	}
}

func hmacSign(secret, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
