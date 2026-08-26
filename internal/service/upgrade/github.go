package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultGithubAPIBase = "https://api.github.com"

// downloadTimeout 对齐 argo 二进制下载的 5 分钟上限：Release 二进制几十
// MB，慢链路 VPS 也需要足够余量。
const downloadTimeout = 5 * time.Minute

// githubClient 是 GitHub Releases 的只读客户端。apiBase 可注入测试假
// 服务。私有仓库依赖 Bearer token；资产下载端点 302 到预签名 URL 时
// net/http 会自动去掉 Authorization 头，无需特殊处理。
type githubClient struct {
	apiBase string
	client  *http.Client
}

func newGithubClient() *githubClient {
	return &githubClient{apiBase: defaultGithubAPIBase, client: &http.Client{Timeout: downloadTimeout}}
}

// releaseAsset 的 Digest 是 GitHub 为 Release 资产自动计算的
// "sha256:<hex>" 摘要（2024-02 后上传的资产必有）。
type releaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type githubRelease struct {
	TagName     string         `json:"tag_name"`
	Body        string         `json:"body"`
	PublishedAt string         `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

func (r githubRelease) asset(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

func (g *githubClient) latestRelease(ctx context.Context, repo, token string) (githubRelease, error) {
	req, err := g.newRequest(ctx, repo, token, "application/vnd.github+json", "/repos/"+repo+"/releases/latest")
	if err != nil {
		return githubRelease{}, err
	}
	response, err := g.client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return githubRelease{}, responseError("查询最新版本", response)
	}
	var release githubRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return githubRelease{}, fmt.Errorf("解析最新版本响应失败: %w", err)
	}
	if release.TagName == "" {
		return githubRelease{}, fmt.Errorf("最新版本响应缺少 tag_name")
	}
	return release, nil
}

func (g *githubClient) downloadAsset(ctx context.Context, repo, token string, assetID int64, dst io.Writer) error {
	req, err := g.newRequest(ctx, repo, token, "application/octet-stream", "/repos/"+repo+"/releases/assets/"+strconv.FormatInt(assetID, 10))
	if err != nil {
		return err
	}
	response, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return responseError("下载资产", response)
	}
	_, err = io.Copy(dst, response.Body)
	return err
}

func (g *githubClient) newRequest(ctx context.Context, repo, token, accept, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.apiBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

func responseError(action string, response *http.Response) error {
	snippet := ""
	if response.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 200))
		snippet = strings.TrimSpace(string(raw))
	}
	if snippet != "" {
		return fmt.Errorf("%s返回 %d: %s", action, response.StatusCode, snippet)
	}
	return fmt.Errorf("%s返回 %d", action, response.StatusCode)
}
