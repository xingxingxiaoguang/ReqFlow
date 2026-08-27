package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// metadataCatalog GET /api/metadata → 元数据目录总览（任务类型列表 + source）。
func (h *handlers) metadataCatalog(c *gin.Context) {
	ok(c, h.svc.Metadata.Catalog())
}

// metadataTaskType GET /api/metadata/task-types/:type → 任务类型聚合视图
// （workflow + schema + profile + 工具清单）。
func (h *handlers) metadataTaskType(c *gin.Context) {
	view, err := h.svc.Metadata.TaskTypeView(c.Param("type"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, view)
}

// metadataPromptPreview POST /api/metadata/render/preview {task_type, special_requirements?}
// → 三段提示词实时渲染（与运行时装配同一函数，元数据页预览 tab 用）。
func (h *handlers) metadataPromptPreview(c *gin.Context) {
	var req app.PromptPreviewInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（task_type 必填）")
		return
	}
	preview, err := h.svc.Metadata.PromptPreview(req)
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, preview)
}
