package web

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/keepon-online/sb-ops/internal/config"
	"github.com/keepon-online/sb-ops/internal/httpx"

	"github.com/gin-gonic/gin"
)

type devProxyAssets struct {
	config *config.RuntimeConfigHolder
}

func newDevProxyAssets(cfg *config.RuntimeConfigHolder) FrontendAssets {
	return &devProxyAssets{config: cfg}
}

func (a *devProxyAssets) Serve(c *gin.Context) {
	target, err := url.Parse(a.config.Current().Development.FrontendDevProxy)
	if err != nil {
		httpx.Error(c, http.StatusInternalServerError, httpx.CodeInternalError, "开发代理地址无效", nil)
		return
	}
	httputil.NewSingleHostReverseProxy(target).ServeHTTP(c.Writer, c.Request)
}
