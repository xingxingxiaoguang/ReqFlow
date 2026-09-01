package httpgin

import "github.com/gin-gonic/gin"

// health GET /api/health
func (h *handlers) health(c *gin.Context) {
	ok(c, gin.H{"status": "up"})
}
