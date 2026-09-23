package egressgeo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckVerdicts(t *testing.T) {
	cases := []struct {
		name         string
		handler      http.HandlerFunc
		wantVerdict  string
		wantRedirect string
		wantErr      bool
	}{
		{
			name: "跳转 google.com.hk 判误判",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://www.google.com.hk/url?sa=p&pref=hkredirect&q=https://www.google.com.hk/")
				w.WriteHeader(http.StatusFound)
			},
			wantVerdict:  VerdictMisjudged,
			wantRedirect: "https://www.google.com.hk/url?sa=p&pref=hkredirect&q=https://www.google.com.hk/",
		},
		{
			name: "跳转裸域 google.com.hk 判误判",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://google.com.hk/")
				w.WriteHeader(http.StatusFound)
			},
			wantVerdict:  VerdictMisjudged,
			wantRedirect: "https://google.com.hk/",
		},
		{
			name: "200 直达判正常",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantVerdict: VerdictOK,
		},
		{
			name: "跳转 consent 同意页不判误判",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://consent.google.com/m?continue=https://www.google.com/")
				w.WriteHeader(http.StatusFound)
			},
			wantVerdict: VerdictOK,
		},
		{
			name: "相对路径重定向按探测目标解析主机",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/url?q=test")
				w.WriteHeader(http.StatusMovedPermanently)
			},
			wantVerdict: VerdictOK,
		},
		{
			name: "5xx 视为探测错误",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.handler)
			defer server.Close()
			verdict, redirect, err := Check(context.Background(), NewClient(), server.URL)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望错误，得到 verdict=%q redirect=%q", verdict, redirect)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望错误: %v", err)
			}
			if verdict != tc.wantVerdict {
				t.Fatalf("判定 = %q, 期望 %q", verdict, tc.wantVerdict)
			}
			if redirect != tc.wantRedirect {
				t.Fatalf("重定向 = %q, 期望 %q", redirect, tc.wantRedirect)
			}
		})
	}
}

func TestCheckNetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	if _, _, err := Check(context.Background(), NewClient(), url); err == nil {
		t.Fatal("期望网络错误")
	}
}

func TestCheckClientDoesNotFollowRedirects(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/landed" {
			seen = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Location", "/landed")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := NewClient()
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码 = %d, 期望不跟随重定向保持 302", resp.StatusCode)
	}
	if seen {
		t.Fatal("客户端不应跟随重定向")
	}
}

func TestStateKeepsVerdictOnFailure(t *testing.T) {
	state := NewState()
	if snapshot := state.Snapshot(); snapshot.Verdict != "" {
		t.Fatalf("初始判定 = %q, 期望空串", snapshot.Verdict)
	}
	state.Record(VerdictMisjudged, "https://www.google.com.hk/")
	first := state.Snapshot()
	if first.Verdict != VerdictMisjudged || first.LastError != "" || first.CheckedAt.IsZero() {
		t.Fatalf("Record 后快照异常: %+v", first)
	}
	time.Sleep(time.Millisecond)
	state.RecordError(errors.New("拨号超时"))
	sticky := state.Snapshot()
	if sticky.Verdict != VerdictMisjudged || sticky.Redirect == "" || sticky.CheckedAt != first.CheckedAt {
		t.Fatalf("失败应粘滞上一个判定: %+v", sticky)
	}
	if sticky.LastError == "" || !sticky.LastAttempt.After(first.LastAttempt) {
		t.Fatalf("失败应记录 LastError/LastAttempt: %+v", sticky)
	}
	state.Record(VerdictOK, "")
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictOK || snapshot.Redirect != "" || snapshot.LastError != "" {
		t.Fatalf("恢复后快照异常: %+v", snapshot)
	}
}
