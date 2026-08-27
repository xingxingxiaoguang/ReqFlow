package httpgin

// 元数据受控编辑端点（M3）：兼容检查 dry-run / schema·profile 保存（守卫）/ 回退 /
// 版本历史 / 导出导入。写路径是显式管理动作（前端仅元数据页入口），每次写必记审计。

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// failWith fail 的带载荷变体：守卫拦截（409）时把判定明细/影响面一并带回前端。
func failWith(c *gin.Context, code int, message string, payload gin.H) {
	body := gin.H{"success": false, "error": message}
	for k, v := range payload {
		body[k] = v
	}
	c.JSON(code, body)
}

// metadataSchemaCheck POST /api/metadata/schemas/:type/check {schema}
// → 兼容性 dry-run（规则表判定 + 存量数据集影响面；不落库）。
func (h *handlers) metadataSchemaCheck(c *gin.Context) {
	var req app.SchemaUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（schema 必填）")
		return
	}
	req.DatasetType = c.Param("type")
	res, err := h.svc.Metadata.CheckSchema(c.Request.Context(), req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, res)
}

// metadataSchemaUpdate PUT /api/metadata/schemas/:type {schema, confirm_risky?, summary?}
// → 受控保存（❌ 拦截 / ⚠️ 需确认；版本递增 + 审计 + effective 刷新）。
func (h *handlers) metadataSchemaUpdate(c *gin.Context) {
	var req app.SchemaUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（schema 必填）")
		return
	}
	req.DatasetType = c.Param("type")
	res, err := h.svc.Metadata.UpdateSchema(c.Request.Context(), req)
	switch {
	case err != nil && res != nil: // 守卫拦截：携带判定明细
		failWith(c, 409, err.Error(), gin.H{"data": res})
	case err != nil:
		fail(c, 404, err.Error())
	default:
		ok(c, res)
	}
}

// metadataSchemaReset DELETE /api/metadata/schemas/:type → 回退到内置（版本历史保留）。
func (h *handlers) metadataSchemaReset(c *gin.Context) {
	res, err := h.svc.Metadata.ResetSchema(c.Request.Context(), c.Param("type"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, res)
}

// metadataProfileUpdate PUT /api/metadata/profiles/:type {role, example, summary?}。
func (h *handlers) metadataProfileUpdate(c *gin.Context) {
	var req app.ProfileUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（role 必填）")
		return
	}
	req.TaskType = c.Param("type")
	res, err := h.svc.Metadata.UpdateProfile(c.Request.Context(), req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, res)
}

// metadataProfileReset DELETE /api/metadata/profiles/:type → 回退到内置。
func (h *handlers) metadataProfileReset(c *gin.Context) {
	res, err := h.svc.Metadata.ResetProfile(c.Request.Context(), c.Param("type"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, res)
}

// metadataHistory GET /api/metadata/history/:kind/:key → 版本历史（新→旧，含载荷原文）。
func (h *handlers) metadataHistory(c *gin.Context) {
	versions, err := h.svc.Metadata.History(c.Request.Context(), c.Param("kind"), c.Param("key"))
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, versions)
}

// metadataExport GET /api/metadata/export → effective 视图导出（JSON 文档）。
func (h *handlers) metadataExport(c *gin.Context) {
	doc, err := h.svc.Metadata.Export()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, doc)
}

// metadataImport POST /api/metadata/import {task_types:[{type, schema?, profile?}], confirm_risky?}
// → 逐项走同一守卫（单项失败不中断）。
func (h *handlers) metadataImport(c *gin.Context) {
	var req app.MetadataImportInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（task_types 必填）")
		return
	}
	res, err := h.svc.Metadata.Import(c.Request.Context(), req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, res)
}
