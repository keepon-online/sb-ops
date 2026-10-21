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
	for range 3 {
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
	for range 3 {
		payload := <-body
		if payload["msg_type"] == "interactive" || payload["msgtype"] == "markdown" {
			t.Fatalf("空载荷不应发送卡片: %v", payload)
		}
	}
}

func TestDomesticNotifierRendersDailyReportCards(t *testing.T) {
	requests := make(chan map[string]any, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		decoded["path"] = r.URL.Path
		requests <- decoded
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
		return DomesticConfig{FeishuWebhookURL: server.URL + "/feishu", DingTalkWebhookURL: server.URL + "/ding", WeComWebhookURL: server.URL + "/wecom"}, nil
	})
	report := Notification{
		EventType: "report.daily",
		Payload:   json.RawMessage(`{"title":"📊 每日运行报告 2026-09-05","message":"昨日流量：上行 1.2 GB · 下行 8.3 GB\n活跃用户：4"}`),
	}
	if err := notifier.Notify(context.Background(), report); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	seen := map[string]bool{}
	for range 3 {
		payload := <-requests
		path, _ := payload["path"].(string)
		seen[path] = true
		switch path {
		case "/feishu":
			card, _ := payload["card"].(map[string]any)
			header, _ := card["header"].(map[string]any)
			title, _ := header["title"].(map[string]any)
			if payload["msg_type"] != "interactive" || header["template"] != "blue" {
				t.Fatalf("feishu 报告卡片应为蓝色模板: %v", payload)
			}
			if !strings.Contains(title["content"].(string), "每日运行报告") {
				t.Fatalf("feishu 报告标题缺失: %v", title)
			}
		case "/ding":
			if payload["msgtype"] != "markdown" {
				t.Fatalf("dingtalk 报告应为 markdown: %v", payload)
			}
			markdown, _ := payload["markdown"].(map[string]any)
			if !strings.Contains(markdown["title"].(string), "每日运行报告") || !strings.Contains(markdown["text"].(string), "活跃用户：4") {
				t.Fatalf("dingtalk 报告内容缺失: %v", markdown)
			}
		case "/wecom":
			if payload["msgtype"] != "markdown" {
				t.Fatalf("wecom 报告应为 markdown: %v", payload)
			}
			markdown, _ := payload["markdown"].(map[string]any)
			if !strings.Contains(markdown["content"].(string), "每日运行报告") || !strings.Contains(markdown["content"].(string), "昨日流量") {
				t.Fatalf("wecom 报告内容缺失: %v", markdown)
			}
		}
	}
	for _, path := range []string{"/feishu", "/ding", "/wecom"} {
		if !seen[path] {
			t.Fatalf("channel %s was not called", path)
		}
	}
}

func TestDomesticNotifierRejectsBusinessErrorCode(t *testing.T) {
	// 业务失败回 HTTP 200 是三家平台的真实契约（签名错、IP 白名单拒绝、
	// 机器人失效、频控），载荷键名按平台真实响应：飞书 code/msg，钉钉/
	// 企微 errcode/errmsg。曾因只看状态码把这类拒绝当成功，静默吞掉一周
	// 每日报告。
	cases := []struct {
		name    string
		config  func(base string) DomesticConfig
		reply   string
		wantErr string
	}{
		{
			name:    "飞书签名失败",
			config:  func(base string) DomesticConfig { return DomesticConfig{FeishuWebhookURL: base + "/feishu"} },
			reply:   `{"code":19021,"msg":"sign match fail"}`,
			wantErr: "code=19021",
		},
		{
			name:    "钉钉签名失败",
			config:  func(base string) DomesticConfig { return DomesticConfig{DingTalkWebhookURL: base + "/dingtalk"} },
			reply:   `{"errcode":310000,"errmsg":"sign not match"}`,
			wantErr: "code=310000",
		},
		{
			name:    "企业微信拒绝",
			config:  func(base string) DomesticConfig { return DomesticConfig{WeComWebhookURL: base + "/wecom"} },
			reply:   `{"errcode":45002,"errmsg":"api forbidden"}`,
			wantErr: "code=45002",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(testCase.reply))
			}))
			defer server.Close()
			notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
				return testCase.config(server.URL), nil
			})
			err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)})
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("err=%v, want contains %q", err, testCase.wantErr)
			}
		})
	}
}

func TestDomesticNotifierAcceptsSuccessAndLenientBodies(t *testing.T) {
	replies := []string{
		// 飞书成功响应的真实形状（StatusCode 与 code 并存且均为 0）。
		`{"StatusCode":0,"StatusMessage":"success","code":0,"data":{},"msg":"success"}`,
		// 钉钉/企微成功。
		`{"errcode":0,"errmsg":"ok"}`,
		// 非 JSON 成功体（空体/纯文本，如反代返回）宽松放行，只拦截可确信的业务失败。
		``,
		`ok`,
	}
	for _, reply := range replies {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(reply))
		}))
		notifier := NewDomesticNotifier(func(context.Context) (DomesticConfig, error) {
			return DomesticConfig{FeishuWebhookURL: server.URL + "/feishu", DingTalkWebhookURL: server.URL + "/ding", WeComWebhookURL: server.URL + "/wecom"}, nil
		})
		if err := notifier.Notify(context.Background(), Notification{Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatalf("reply %q: err=%v", reply, err)
		}
		server.Close()
	}
}

func TestDomesticNotifierContinuesAfterBusinessError(t *testing.T) {
	called := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- r.URL.Path
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/feishu" {
			_, _ = w.Write([]byte(`{"code":19021,"msg":"sign match fail"}`))
			return
		}
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
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
