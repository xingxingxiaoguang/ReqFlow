package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// importStream POST /api/import {record_id, project_id, items} → SSE。
// 逐条推送导入进度；project_id 形如 "new:项目名" 时自动创建项目。
func (h *handlers) importStream(c *gin.Context) {
	var req struct {
		RecordID  string                  `json:"record_id" binding:"required"`
		ProjectID string                  `json:"project_id" binding:"required"`
		Items     []app.ImportItemInput   `json:"items" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "导入参数不完整")
		return
	}

	in := app.ImportInput{RecordID: req.RecordID, ProjectID: req.ProjectID, Items: req.Items}

	startSSE(c)
	sendEvent(c, "progress", gin.H{
		"stage": "importing", "current": 0, "total": len(in.Items),
		"message": "开始导入…",
	})

	res, err := h.svc.Import.Run(c.Request.Context(), in, func(p app.ImportProgress) {
		if clientGone(c) {
			return
		}
		sendEvent(c, "progress", gin.H{
			"stage": "importing", "current": p.Current, "total": p.Total,
			"title": p.Title, "status": p.Status, "message": p.Message,
		})
	})
	if err != nil {
		sendEvent(c, "error", gin.H{"message": err.Error()})
		return
	}

	created := make([]gin.H, len(res.CreatedProjects))
	for i, p := range res.CreatedProjects {
		created[i] = gin.H{"id": p.ID, "name": p.Name}
		sendEvent(c, "project_created", gin.H{"id": p.ID, "name": p.Name})
	}
	sendEvent(c, "complete", gin.H{"result": gin.H{
		"success": res.Success, "failed": res.Failed,
		"errors": res.Errors, "createdProjects": created,
		"recordStatus": res.RecordStatus,
	}})
}
