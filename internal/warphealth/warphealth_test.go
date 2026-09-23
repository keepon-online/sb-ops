package warphealth

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newSocksServer 起一个最小 no-auth SOCKS5 服务（RFC1928 连接子集：仅
// CONNECT），CONNECT 请求的 target 与客户端连接交给 handler 处置——测试
// 据此模拟正常管道、挂起（超时）等链路形态。返回监听地址。
func newSocksServer(t *testing.T, handler func(target string, client net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks server: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSocksClient(conn, handler)
		}
	}()
	return listener.Addr().String()
}

func handleSocksClient(client net.Conn, handler func(string, net.Conn)) {
	defer client.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(client, head); err != nil {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(client, methods); err != nil {
		return
	}
	if _, err := client.Write([]byte{5, 0}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(client, request); err != nil {
		return
	}
	if request[1] != 1 {
		return
	}
	var host string
	switch request[3] {
	case 1:
		raw := make([]byte, 4)
		if _, err := io.ReadFull(client, raw); err != nil {
			return
		}
		host = net.IP(raw).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(client, length); err != nil {
			return
		}
		raw := make([]byte, int(length[0]))
		if _, err := io.ReadFull(client, raw); err != nil {
			return
		}
		host = string(raw)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(client, port); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port))))
	handler(target, client)
}

// pipeTarget 接管 CONNECT：拨号 target 并双向拷贝（TLS 字节流原样透传）。
func pipeTarget(target string, client net.Conn) {
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = client.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err := client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
	}()
	_, _ = io.Copy(client, upstream)
}

// hangTarget 模拟隧道黑洞：完成 SOCKS 握手后不回包也不转发，制造读超时。
func hangTarget(target string, client net.Conn) {
	_, _ = client.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	select {}
}

// newTLSUpstream 起一个自签 TLS 上游：/cdn-cgi/trace 回 traceBody，
// / 回 googleStatus/location。经 newSocksTransport 拨入时需跳过证书校验。
func newTLSUpstream(t *testing.T, traceBody, googleStatus, googleLocation string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/cdn-cgi/trace", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(traceBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if googleLocation != "" {
			w.Header().Set("Location", googleLocation)
		}
		w.WriteHeader(statusCode(googleStatus))
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}

func statusCode(name string) int {
	switch name {
	case "200":
		return http.StatusOK
	case "302":
		return http.StatusFound
	default:
		return http.StatusOK
	}
}

func testClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	transport, err := newSocksTransport(proxyAddr)
	if err != nil {
		t.Fatalf("newSocksTransport: %v", err)
	}
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func TestCheckGoogleVerdicts(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		location string
		want     string
	}{
		{name: "直通 200 判正常", status: "200", want: VerdictOK},
		{name: "跳 google.com.hk 判误判", status: "302", location: "https://www.google.com.hk/url?sa=p&hl=zh-CN", want: VerdictMisjudged},
		{name: "跳子域 google.com.hk 同判误判", status: "302", location: "https://www.google.com.hk/", want: VerdictMisjudged},
		{name: "其它重定向（同意页）判正常", status: "302", location: "https://consent.google.com/m?continue=x", want: VerdictOK},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := newTLSUpstream(t, "", testCase.status, testCase.location)
			socksAddr := newSocksServer(t, pipeTarget)
			client := testClient(t, socksAddr)
			verdict, err := checkGoogle(context.Background(), client, upstream.URL+"/")
			if err != nil {
				t.Fatalf("checkGoogle: %v", err)
			}
			if verdict != testCase.want {
				t.Fatalf("verdict=%q want %q", verdict, testCase.want)
			}
		})
	}
}

func TestFetchTraceParsesWarpAndFamily(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantOn     bool
		wantFamliy string
		wantErr    bool
	}{
		{name: "MASQUE IPv6 出口", body: "ip=2a09:bac5:6214:1232::1d0:b4\nwarp=on\nloc=US\n", wantOn: true, wantFamliy: "ipv6"},
		{name: "IPv4 出口 warp 关闭", body: "ip=162.159.192.1\nwarp=off\nloc=US\n", wantOn: false, wantFamliy: "ipv4"},
		{name: "缺 warp 字段不可解析", body: "ip=1.2.3.4\nloc=US\n", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := newTLSUpstream(t, testCase.body, "200", "")
			socksAddr := newSocksServer(t, pipeTarget)
			client := testClient(t, socksAddr)
			warpOn, family, err := fetchTrace(context.Background(), client, upstream.URL+"/cdn-cgi/trace")
			if testCase.wantErr {
				if err == nil {
					t.Fatal("want parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchTrace: %v", err)
			}
			if warpOn != testCase.wantOn || family != testCase.wantFamliy {
				t.Fatalf("warpOn=%v family=%q want %v/%q", warpOn, family, testCase.wantOn, testCase.wantFamliy)
			}
		})
	}
}

func TestCheckProxyDownIsDefinitive(t *testing.T) {
	// 未监听端口：判定 proxy-down 且非 error（终态直接更新 state，不粘滞）。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	// WSL2 mirrored 等环境对已关闭端口的 loopback 拨号可能假成功（TCP 握手
	// 完成后随即 RST），该环境下无法稳定构造「代理不可达」形态，跳过；
	// 常规环境（含 CI）此拨号应失败。
	if probe, dialErr := net.DialTimeout("tcp", addr, time.Second); dialErr == nil {
		_ = probe.Close()
		t.Skip("环境对已关闭端口假成功（如 WSL mirrored loopback），跳过")
	}
	result, err := Check(context.Background(), addr)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result.Verdict != VerdictProxyDown || !strings.Contains(result.Reason, addr) {
		t.Fatalf("result=%+v", result)
	}
}

func TestCheckAmbiguousFailureSurfacesAsError(t *testing.T) {
	// 隧道黑洞（握手成功后挂起）：Check 返回 error，由调用方粘滞上一判定。
	socksAddr := newSocksServer(t, hangTarget)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := Check(ctx, socksAddr); err == nil {
		t.Fatal("黑洞隧道应返回 error 供粘滞处理")
	}
}

func TestStateStickyAndReset(t *testing.T) {
	state := NewState()
	// 无检查记录：Verdict 为空（门控视图返回 ok=false）。
	if snapshot := state.Snapshot(); snapshot.Verdict != "" {
		t.Fatalf("初始 Verdict=%q want 空", snapshot.Verdict)
	}
	state.Record(Result{Verdict: VerdictOK, WarpOn: true, ExitFamily: "ipv6"})
	// 歧义失败粘滞：判定与原因保持，错误文本与最近尝试更新。
	before := state.Snapshot()
	state.RecordUnknown(contextDeadlineError())
	after := state.Snapshot()
	if after.Verdict != VerdictOK || after.Reason != "" || after.ExitFamily != "ipv6" {
		t.Fatalf("粘滞失败: %+v", after)
	}
	if after.LastError == "" || !after.LastAttempt.After(before.LastAttempt) {
		t.Fatalf("未知失败未记录: %+v", after)
	}
	// 确定性终态覆盖：misjudged 连同原因一起更新。
	state.Record(Result{Verdict: VerdictMisjudged, Reason: "出口被判 CN 区"})
	if snapshot := state.Snapshot(); snapshot.Verdict != VerdictMisjudged || snapshot.Reason == "" {
		t.Fatalf("终态覆盖失败: %+v", snapshot)
	}
	// 门控关闭 Reset：回到未检测。
	state.Reset()
	if snapshot := state.Snapshot(); snapshot.Verdict != "" || snapshot.LastError != "" {
		t.Fatalf("Reset 失败: %+v", snapshot)
	}
}

func contextDeadlineError() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	return ctx.Err()
}
