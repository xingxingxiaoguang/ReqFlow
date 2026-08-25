package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// syncStream POST /api/sync → SSE。增量同步平台项目/工作项/元数据到本地与向量库。
func (h *handlers) syncStream(c *gin.Context) {
	startSSE(c)
	sendEvent(c, "progress", gin.H{"stage": "projects", "status": "running", "message": "开始同步…"})

	res, err := h.svc.Sync.Run(c.Request.Context(), func(p app.SyncProgress) {
		if clientGone(c) {
			return
		}
		sendEvent(c, "progress", gin.H{"stage": p.Stage, "status": "running", "message": p.Message})
	})
	if err != nil {
		sendEvent(c, "error", gin.H{"message": err.Error()})
		return
	}
	sendEvent(c, "complete", gin.H{"result": res})
}
