package web

import (
	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/config"
)

func RegisterFrontendRoutes(router *gin.Engine, cfg *config.RuntimeConfigHolder) {
	registerStaticRoutes(router, newEmbeddedAssets(cfg))
}
