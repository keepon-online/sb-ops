package singbox

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultWarpRegisterURL is the unofficial Cloudflare WARP client registration
// endpoint (the same family used by wgcf / fscarmen / yonggekkk scripts). The
// path segment v0a2158 matches the CF-Client-Version below; changing one
// usually requires changing the other.
const defaultWarpRegisterURL = "https://api.cloudflareclient.com/v0a2158/reg"

const warpRegisterTimeout = 30 * time.Second

// warpRegResponse is the subset of the registration reply the panel uses.
type warpRegResponse struct {
	Config struct {
		ClientID string `json:"client_id"`
		Peers    []struct {
			PublicKey string `json:"public_key"`
		} `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// RegisterWarp creates a free WARP account on the fly: it generates a fresh
// curve25519 key pair, registers the public half with the Cloudflare client
// API and returns ready-to-save outbound settings. The caller persists them
// through the normal outbound write path — nothing is stored here.
func (s *Service) RegisterWarp(ctx context.Context) (WarpSettings, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return WarpSettings{}, fmt.Errorf("生成 WARP 密钥对失败: %w", err)
	}
	settings, err := registerWarpKey(ctx, s.warpRegisterURL, base64.StdEncoding.EncodeToString(private.PublicKey().Bytes()))
	if err != nil {
		return WarpSettings{}, err
	}
	settings.PrivateKey = base64.StdEncoding.EncodeToString(private.Bytes())
	return settings, nil
}

func registerWarpKey(ctx context.Context, registerURL, publicKey string) (WarpSettings, error) {
	requestBody, err := json.Marshal(map[string]string{
		"key": publicKey, "install_id": "", "fcm": "", "model": "Linux", "serial_number": "", "locale": "en_US",
	})
	if err != nil {
		return WarpSettings{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, warpRegisterTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(requestBody))
	if err != nil {
		return WarpSettings{}, err
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "curl/8.0.1")
	request.Header.Set("CF-Client-Version", "a-7.21-0729")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return WarpSettings{}, fmt.Errorf("请求 WARP 注册接口失败（本机需能直连 api.cloudflareclient.com）: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return WarpSettings{}, fmt.Errorf("读取 WARP 注册响应失败: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return WarpSettings{}, fmt.Errorf("WARP 注册接口返回 %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseWarpRegistration(body)
}

func parseWarpRegistration(body []byte) (WarpSettings, error) {
	var reply warpRegResponse
	if err := json.Unmarshal(body, &reply); err != nil {
		return WarpSettings{}, fmt.Errorf("解析 WARP 注册响应失败: %w", err)
	}
	config := reply.Config
	if len(config.Peers) == 0 || config.Peers[0].PublicKey == "" {
		return WarpSettings{}, fmt.Errorf("WARP 注册响应缺少对端公钥")
	}
	if config.Interface.Addresses.V4 == "" {
		return WarpSettings{}, fmt.Errorf("WARP 注册响应缺少 IPv4 地址")
	}
	settings := WarpSettings{
		Endpoint:  DefaultWarpEndpoint,
		PublicKey: config.Peers[0].PublicKey,
		IPv4:      cidrify(config.Interface.Addresses.V4, 32),
		IPv6:      cidrify(config.Interface.Addresses.V6, 128),
		Reserved:  warpReservedFromClientID(config.ClientID),
	}
	return settings, nil
}

// cidrify 给 Cloudflare 返回的裸地址补上前缀（v4→/32、v6→/128），
// 让 sing-box wireguard 端点与面板校验（netip.ParsePrefix）都能接受。
func cidrify(address string, bits int) string {
	if address == "" || strings.Contains(address, "/") {
		return address
	}
	return fmt.Sprintf("%s/%d", address, bits)
}

// warpReservedFromClientID derives the three wireguard "reserved" bytes from
// the account's client_id, the convention shared by wgcf-family tools: base64
// decode and keep the leading three bytes. A short or undecodable client_id
// yields no reserved bytes — free-tier handshakes still succeed without them.
func warpReservedFromClientID(clientID string) []int {
	if len(clientID) < 3 {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(clientID)
	if err != nil || len(raw) < 3 {
		raw = []byte(clientID)
	}
	return []int{int(raw[0]), int(raw[1]), int(raw[2])}
}
