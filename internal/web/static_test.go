package web

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

// 静态资源缓存策略：index.html 与 SPA 回退必须回源验证（否则浏览器沿用旧
// index.html 跑旧前端），带内容 hash 的 assets 可长缓存。
func TestEmbeddedAssetsCacheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	files := fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<html>ok</html>")},
		"assets/index-abc123.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	assets := &embeddedAssets{files: files}
	router := gin.New()
	router.NoRoute(assets.Serve)

	cases := []struct {
		path      string
		wantCache string
	}{
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/users", "no-cache"}, // SPA 回退 index.html
		{"/assets/index-abc123.js", "public, max-age=31536000, immutable"},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", tc.path, nil))
		if got := recorder.Header().Get("Cache-Control"); got != tc.wantCache {
			t.Errorf("%s Cache-Control=%q, want %q", tc.path, got, tc.wantCache)
		}
	}
}
