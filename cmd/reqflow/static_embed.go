//go:build embed

// 发布构建（make build → go build -tags embed）：前端产物打进二进制，单文件分发。
package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed all:dist
var distFS embed.FS

// mountStatic 挂载前端静态资源与 SPA fallback（Makefile 先把 web/dist 拷贝到本目录）。
// 不走 http.FileServer：它会把 /index.html 请求 301 规范化到 ./，破坏 SPA 直出。
func mountStatic(r *gin.Engine) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "接口不存在"})
			return
		}
		name := strings.TrimPrefix(path, "/")
		if name == "" {
			name = "index.html"
		}
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			// 静态文件未命中 → SPA 路由，回退 index.html
			name = "index.html"
			if data, err = fs.ReadFile(sub, name); err != nil {
				c.String(http.StatusNotFound, "前端资源缺失")
				return
			}
		}

		ct := mime.TypeByExtension(filepath.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		if strings.Contains(ct, "text/html") {
			c.Header("Cache-Control", "no-cache")
		} else {
			// 带 hash 的静态资源可长缓存
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Data(http.StatusOK, ct, data)
	})
}
