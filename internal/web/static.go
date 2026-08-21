package web

import (
	"io/fs"
	"mime"
	stdhttp "net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/httpx"

	"github.com/gin-gonic/gin"
)

type FrontendAssets interface {
	Serve(c *gin.Context)
}

func registerStaticRoutes(router *gin.Engine, assets FrontendAssets) {
	router.NoRoute(func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if requestPath == "/api" || strings.HasPrefix(requestPath, "/api/") ||
			requestPath == "/agent" || strings.HasPrefix(requestPath, "/agent/") {
			httpx.Error(c, stdhttp.StatusNotFound, httpx.CodeNotFound, "接口不存在", nil)
			return
		}
		assets.Serve(c)
	})
}

func newFrontendAssets(cfg *config.RuntimeConfigHolder) FrontendAssets {
	if cfg != nil && strings.TrimSpace(cfg.Current().Development.FrontendDevProxy) != "" {
		return newDevProxyAssets(cfg)
	}
	return newEmbeddedAssets(cfg)
}

type embeddedAssets struct {
	config *config.RuntimeConfigHolder
	files  fs.FS
}

func newEmbeddedAssets(cfg *config.RuntimeConfigHolder) FrontendAssets {
	return &embeddedAssets{
		config: cfg,
		files:  embeddedFrontendAssets(),
	}
}

func (a *embeddedAssets) Serve(c *gin.Context) {
	requestPath := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
	if requestPath == "." {
		requestPath = ""
	}

	if requestPath != "" {
		if body, err := fs.ReadFile(a.files, requestPath); err == nil {
			contentType := mime.TypeByExtension(filepath.Ext(requestPath))
			if contentType == "" {
				contentType = stdhttp.DetectContentType(body)
			}
			// Vite 产物带内容 hash，可安全长缓存；其余文件（含 404 回退前的
			// 未知路径）必须回源验证，否则浏览器会沿用旧 index.html 跑旧前端。
			if strings.HasPrefix(requestPath, "assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				c.Header("Cache-Control", "no-cache")
			}
			c.Data(stdhttp.StatusOK, contentType, body)
			return
		}
	}

	body, err := fs.ReadFile(a.files, "index.html")
	if err != nil {
		httpx.Error(c, stdhttp.StatusNotFound, httpx.CodeNotFound, "前端页面不存在", nil)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(stdhttp.StatusOK, "text/html; charset=utf-8", body)
}
