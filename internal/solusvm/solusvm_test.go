package solusvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// liveInfoBody 按 2026-09 对 RackNerd 面板（迁移前 nerdvm.racknerd.com）真实
// 调用响应的形状构造（IP 与主机名已换文档保留段；夹具按真实载荷写，勿照解析
// 器改）：2TB 套餐，bw 四元组字节单位、顺序 总量,已用,剩余,百分比，
// 剩余=总量-已用 精确闭合。
const liveInfoBody = "<ipaddr>203.0.113.10</ipaddr><hdd>16106127360,0,16106127360,0</hdd><bw>2147483648000,25292189863,2122191458137,1</bw><mem>0,0,0,0</mem><status>success</status><statusmsg></statusmsg><hostname>racknerd-example</hostname><ipaddress>203.0.113.10</ipaddress><vmstat></vmstat>"

func TestParseInfoLivePayload(t *testing.T) {
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	sample, err := ParseInfo(liveInfoBody, at)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sample.TotalBytes != 2147483648000 || sample.UsedBytes != 25292189863 || sample.FreeBytes != 2122191458137 {
		t.Fatalf("sample=%+v", sample)
	}
	if sample.TotalBytes != sample.UsedBytes+sample.FreeBytes {
		t.Fatalf("总量须等于已用+剩余：total=%d used=%d free=%d", sample.TotalBytes, sample.UsedBytes, sample.FreeBytes)
	}
	if sample.At != at {
		t.Fatalf("at=%v want %v", sample.At, at)
	}
	if want := float64(25292189863) / float64(2147483648000) * 100; sample.Percent < want-0.001 || sample.Percent > want+0.001 {
		t.Fatalf("percent=%v want %v", sample.Percent, want)
	}
}

func TestParseInfoRejectsBadPayloads(t *testing.T) {
	at := time.Now().UTC()
	for name, body := range map[string]string{
		"认证失败":   "<status>error</status><statusmsg>Invalid key or hash</statusmsg>",
		"无状态":    "<ipaddr>1.2.3.4</ipaddr>",
		"缺bw":    "<status>success</status><statusmsg></statusmsg>",
		"bw三元组":  "<status>success</status><bw>100,10,90</bw>",
		"bw非数值":  "<status>success</status><bw>100,abc,90,1</bw>",
		"状态错误消息": "<status>error</status><statusmsg></statusmsg>",
	} {
		if _, err := ParseInfo(body, at); err == nil {
			t.Fatalf("%s：应返回错误", name)
		}
	}
}

func TestClientPostsCredentialsAndParses(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		gotForm = r.PostForm
		_, _ = w.Write([]byte(liveInfoBody))
	}))
	defer server.Close()

	client := NewClient(server.URL, "key-1", "hash-1")
	sample, err := client.Info(t.Context())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if sample.TotalBytes != 2147483648000 {
		t.Fatalf("totalBytes=%d", sample.TotalBytes)
	}
	// 凭据只经 POST 表单提交（不进 URL/访问日志）。
	for field, want := range map[string]string{"key": "key-1", "hash": "hash-1", "action": "info", "bw": "true"} {
		if gotForm.Get(field) != want {
			t.Fatalf("form %s=%q want %q", field, gotForm.Get(field), want)
		}
	}
}

func TestClientSurfacesAPIAndHTTPErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "k", "h").Info(t.Context()); err == nil {
		t.Fatal("HTTP 错误应返回错误")
	}

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<status>error</status><statusmsg>Invalid key or hash</statusmsg>"))
	}))
	defer authServer.Close()
	_, err := NewClient(authServer.URL, "k", "h").Info(t.Context())
	if err == nil {
		t.Fatal("API error 状态应返回错误")
	}
}

func TestStateKeepsLastSuccessOnFailure(t *testing.T) {
	state := NewState()
	if snapshot := state.Snapshot(); snapshot.Sample != nil || snapshot.LastError != "" {
		t.Fatalf("初始快照应为空：%+v", snapshot)
	}
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	state.Record(Sample{TotalBytes: 100, UsedBytes: 10, FreeBytes: 90, Percent: 10, At: at})
	state.RecordError(context.Canceled)
	snapshot := state.Snapshot()
	if snapshot.Sample == nil || snapshot.Sample.UsedBytes != 10 {
		t.Fatalf("失败不应清掉既有采样：%+v", snapshot)
	}
	if snapshot.LastError == "" || !snapshot.LastSuccess.Equal(at) {
		t.Fatalf("快照=%+v", snapshot)
	}
}
