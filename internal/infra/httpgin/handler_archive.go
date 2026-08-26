package httpgin

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// archiveTask DELETE /api/tasks/:id → 任务入归档（含步骤/明细快照；运行中拒绝）。
func (h *handlers) archiveTask(c *gin.Context) {
	if err := h.svc.Archive.ArchiveTask(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"archived": true})
}

// archiveDataset DELETE /api/datasets/:id → 数据集入归档（含条目与向量；被引用拒绝）。
func (h *handlers) archiveDataset(c *gin.Context) {
	if err := h.svc.Archive.ArchiveDataset(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"archived": true})
}

// listArchives GET /api/archives?kind=task|dataset&type=&limit= → 归档列表（不参与主业务循环）。
func (h *handlers) listArchives(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	view, err := h.svc.Archive.List(c.Request.Context(), c.Query("kind"), c.Query("type"), limit)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, view)
}

// restoreArchive POST /api/archives/:kind/:id/restore → 归档恢复到主表。
func (h *handlers) restoreArchive(c *gin.Context) {
	id := c.Param("id")
	switch c.Param("kind") {
	case app.ArchiveKindTask:
		task, err := h.svc.Archive.RestoreTask(c.Request.Context(), id)
		if err != nil {
			fail(c, 409, err.Error())
			return
		}
		ok(c, gin.H{"task": task})
	case app.ArchiveKindDataset:
		if err := h.svc.Archive.RestoreDataset(c.Request.Context(), id); err != nil {
			fail(c, 409, err.Error())
			return
		}
		ok(c, gin.H{"restored": true})
	default:
		fail(c, 400, "不支持的归档类型")
	}
}
