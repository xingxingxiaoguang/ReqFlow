package httpgin

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// requestLogger 极简访问日志（SSE 长连接完成时记录）。
func requestLogger() gin.HandlerFunc {
	logger := slog.Default()
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("http",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"cost_ms", time.Since(start).Milliseconds(),
		)
	}
}
