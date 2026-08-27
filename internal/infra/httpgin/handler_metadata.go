package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// metadataCatalog GET /api/metadata → 元数据目录总览（任务类型列表 + 向导草稿组 + source）。
func (h *handlers) metadataCatalog(c *gin.Context) {
	ok(c, h.svc.Metadata.Catalog(c.Request.Context()))
}

// metadataTaskType GET /api/metadata/task-types/:type → 任务类型聚合视图
// （workflow + schema + profile + 工具清单）。?include_draft=true 时对未生效的
// 向导草稿返回草稿组合视图（验证入口）。
func (h *handlers) metadataTaskType(c *gin.Context) {
	includeDraft := c.Query("include_draft") == "true"
	view, err := h.svc.Metadata.TaskTypeView(c.Param("type"), includeDraft)
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
