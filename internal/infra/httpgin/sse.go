// Package httpgin 入站适配：Gin 路由与 handler。
// 本包只依赖 app 用例（外层进入业务层的唯一入口），不触碰仓储与三方客户端。
package httpgin

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// startSSE 初始化 SSE 响应头。
func startSSE(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // 反代不缓冲
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()
}

// sendEvent 推送一条 SSE 事件并立即 flush。
func sendEvent(c *gin.Context, event string, data any) {
	buf, err := json.Marshal(data)
	if err != nil {
		return
	}
	if _, err := c.Writer.Write([]byte("event: " + event + "\ndata: " + string(buf) + "\n\n")); err != nil {
		return
	}
	c.Writer.Flush()
}

// clientGone 客户端是否已断开。
func clientGone(c *gin.Context) bool {
	select {
	case <-c.Request.Context().Done():
		return true
	default:
		return false
	}
}

// ok 统一 JSON 成功响应。
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// fail 统一 JSON 失败响应。
func fail(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{"success": false, "error": message})
}
