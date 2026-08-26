package httpgin

import (
	"github.com/gin-gonic/gin"
)

// health GET /api/health
func (h *handlers) health(c *gin.Context) {
	ok(c, gin.H{"status": "up"})
}

// overview GET /api/overview 仪表盘概览。
func (h *handlers) overview(c *gin.Context) {
	ov, err := h.svc.Overview.Get(c.Request.Context())
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, ov)
}

// viewSettings GET /api/settings 脱敏配置视图。
func (h *handlers) viewSettings(c *gin.Context) {
	ok(c, h.svc.Settings.View())
}

// testLLM POST /api/settings/test-llm
func (h *handlers) testLLM(c *gin.Context) {
	if err := h.svc.Settings.TestLLM(c.Request.Context()); err != nil {
		fail(c, 502, err.Error())
		return
	}
	ok(c, gin.H{"message": "LLM 连接正常"})
}
