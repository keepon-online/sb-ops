package singlevps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Connection struct {
	ID            string    `json:"id"`
	StartedAt     time.Time `json:"startedAt"`
	Inbound       string    `json:"inbound,omitempty"`
	Rule          string    `json:"rule,omitempty"`
	Outbound      string    `json:"outbound,omitempty"`
	Host          string    `json:"host,omitempty"`
	Port          int       `json:"port,omitempty"`
	Network       string    `json:"network,omitempty"`
	UploadBytes   int64     `json:"uploadBytes"`
	DownloadBytes int64     `json:"downloadBytes"`
}

type ConnectionsResult struct {
	Available bool         `json:"available"`
	Items     []Connection `json:"items"`
	Reason    string       `json:"reason,omitempty"`
}

type clashConnectionResponse struct {
	Connections []struct {
		ID       string `json:"id"`
		Start    string `json:"start"`
		Metadata struct {
			Host    string `json:"host"`
			DstPort string `json:"dstPort"`
			Network string `json:"network"`
			Inbound string `json:"inbound"`
			Type    string `json:"type"`
			Rule    string `json:"rule"`
		} `json:"metadata"`
		Chains   []string `json:"chains"`
		Upload   int64    `json:"upload"`
		Download int64    `json:"download"`
	} `json:"connections"`
}

// clashInboundTag extracts the inbound tag from clash connection metadata.
// sing-box v1.13 has no separate "inbound" field: it packs the inbound into
// "type" as "<inboundType>/<inboundTag>" (e.g. "tuic/tuic-tuic"). The bare
// field is kept first for older cores that expose it directly.
func clashInboundTag(inbound, connectionType string) string {
	if inbound != "" {
		return inbound
	}
	if _, tag, found := strings.Cut(connectionType, "/"); found {
		return strings.TrimSpace(tag)
	}
	return ""
}

func (s *SyncService) Connections(ctx context.Context) (ConnectionsResult, error) {
	endpoint, secret, err := s.clashAPI(ctx)
	if err != nil {
		return ConnectionsResult{Available: false, Items: []Connection{}, Reason: err.Error()}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/connections", nil)
	if err != nil {
		return ConnectionsResult{}, err
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return ConnectionsResult{Available: false, Items: []Connection{}, Reason: "无法连接本地 Clash API"}, nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ConnectionsResult{Available: false, Items: []Connection{}, Reason: fmt.Sprintf("Clash API 返回 %s", response.Status)}, nil
	}
	var payload clashConnectionResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return ConnectionsResult{}, fmt.Errorf("解析 Clash 连接: %w", err)
	}
	items := make([]Connection, 0, len(payload.Connections))
	for _, item := range payload.Connections {
		value := Connection{ID: item.ID, Inbound: clashInboundTag(item.Metadata.Inbound, item.Metadata.Type), Rule: item.Metadata.Rule, Host: item.Metadata.Host, Network: item.Metadata.Network, UploadBytes: item.Upload, DownloadBytes: item.Download}
		if len(item.Chains) > 0 {
			value.Outbound = item.Chains[len(item.Chains)-1]
		}
		_, _ = fmt.Sscanf(item.Metadata.DstPort, "%d", &value.Port)
		value.StartedAt, _ = time.Parse(time.RFC3339Nano, item.Start)
		items = append(items, value)
	}
	return ConnectionsResult{Available: true, Items: items}, nil
}

func (s *SyncService) CloseConnection(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return fmt.Errorf("连接 ID 无效")
	}
	endpoint, secret, err := s.clashAPI(ctx)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/connections/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("关闭 Clash 连接: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return fmt.Errorf("关闭 Clash 连接：API 返回 %s", response.Status)
	}
	return nil
}

// VerifyKernel dials the panel-managed Clash API to detect a kernel that
// reports running but is no longer serving. It returns verified=false when
// the probe cannot run (Clash API disabled or kernel not running); a
// returned error means the kernel claims to be running yet the probe failed.
func (s *SyncService) VerifyKernel(ctx context.Context) (bool, error) {
	if s.runtime.Status().State != "running" {
		return false, nil
	}
	endpoint, secret, err := s.clashAPI(ctx)
	if err != nil {
		return false, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/version", nil)
	if err != nil {
		return true, err
	}
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return true, fmt.Errorf("探测 Clash API 失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return true, fmt.Errorf("探测 Clash API 返回 %s", response.Status)
	}
	return true, nil
}

func clashEndpoint(value string) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(value))
	if err != nil || endpoint.Scheme != "http" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("连接观测尚未配置（Clash）")
	}
	host := endpoint.Hostname()
	if host == "localhost" || net.ParseIP(host) != nil || strings.HasSuffix(host, ".local") || host == "proxy" {
		return strings.TrimRight(endpoint.String(), "/"), nil
	}
	return "", fmt.Errorf("地址必须是本机或内部服务（Clash API）")
}
