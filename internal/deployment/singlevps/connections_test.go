package singlevps

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testClashDB seeds a managed sing-box configuration whose Clash API listens at
// the given address, so connection observation is derived from the panel-managed
// config.
func testClashDB(t *testing.T, listen string) *sql.DB {
	t.Helper()
	db := testSyncDatabase(t)
	if _, err := db.Exec(`INSERT OR IGNORE INTO singbox_config(id,updated_at) VALUES(1,1785196800)`); err != nil {
		t.Fatalf("seed singbox config: %v", err)
	}
	if _, err := db.Exec(`UPDATE singbox_config SET clash_api_enabled=1,clash_api_listen=? WHERE id=1`, listen); err != nil {
		t.Fatalf("set clash listen: %v", err)
	}
	return db
}

func TestConnectionsReadsAllowedLocalClashAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/connections" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"connections":[{"id":"connection-1","start":"2026-08-01T12:00:00Z","metadata":{"host":"example.com","dstPort":"443","network":"tcp","inbound":"vless-in","rule":"Proxy"},"chains":["direct","proxy"],"upload":12,"download":34}]}`))
	}))
	defer server.Close()

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	service := NewSyncService(testClashDB(t, net.JoinHostPort("127.0.0.1", port)), ApplyConfig{})
	result, err := service.Connections(context.Background())
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if !result.Available || len(result.Items) != 1 {
		t.Fatalf("result = %#v", result)
	}
	item := result.Items[0]
	if item.ID != "connection-1" || item.Host != "example.com" || item.Port != 443 || item.Outbound != "proxy" || item.UploadBytes != 12 || item.DownloadBytes != 34 {
		t.Fatalf("connection = %#v", item)
	}
}

func TestConnectionsRejectsExternalClashAPI(t *testing.T) {
	service := NewSyncService(testClashDB(t, "example.com:9090"), ApplyConfig{})
	result, err := service.Connections(context.Background())
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if result.Available || result.Reason == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCloseConnectionUsesSingleEscapedID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.URL.Path != "/connections/connection-1" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	service := NewSyncService(testClashDB(t, net.JoinHostPort("127.0.0.1", port)), ApplyConfig{})
	if err := service.CloseConnection(context.Background(), "connection-1"); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
}
