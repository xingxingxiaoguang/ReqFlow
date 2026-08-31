package httpgin

import "github.com/gin-gonic/gin"

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
