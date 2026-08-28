// 数据集管理与字段全文检索的 HTTP 面。
// 字段定义归属数据集实例：创建（模板带出/自定义）与受控编辑都作用于数据集行上的
// schema；写入守卫与类型级受控编辑同一套兼容引擎（判定口径单一事实源）。
package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// createDataset POST /api/datasets {name, type, description?, tags?, schema?}
// → 新建 ready 空数据集（schema 缺省从类型模板带出，提供时必须合法）。
func (h *handlers) createDataset(c *gin.Context) {
	var req app.CreateDatasetInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（name 与 type 必填）")
		return
	}
	ds, err := h.svc.DatasetAdmin.CreateDataset(c.Request.Context(), req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"dataset": ds})
}

// datasetSchemaCheck POST /api/datasets/:id/schema/check {schema} → 兼容性 dry-run。
func (h *handlers) datasetSchemaCheck(c *gin.Context) {
	var req app.DatasetSchemaUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（schema 必填）")
		return
	}
	report, err := h.svc.DatasetAdmin.CheckDatasetSchema(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"report": report})
}

// datasetSchemaUpdate PUT /api/datasets/:id/schema {schema, confirm_risky?, summary?}
// → 字段定义受控保存（❌ 拦截 / ⚠️ 需确认，携带判定明细）。
func (h *handlers) datasetSchemaUpdate(c *gin.Context) {
	var req app.DatasetSchemaUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（schema 必填）")
		return
	}
	report, err := h.svc.DatasetAdmin.UpdateDatasetSchema(c.Request.Context(), c.Param("id"), req)
	switch {
	case err != nil && (report.Blocked || report.NeedsConfirm): // 守卫拦截：携带判定明细
		failWith(c, 409, err.Error(), gin.H{"data": report})
	case err != nil:
		fail(c, 400, err.Error())
	default:
		ok(c, gin.H{"report": report})
	}
}

// searchDatasetFTS POST /api/datasets/:id/search {q, top_n?} → 字段全文检索
// （PG 表达式 tsvector，命中随 schema 动态建的 FTS 索引；按相关度排序）。
func (h *handlers) searchDatasetFTS(c *gin.Context) {
	var req struct {
		Q    string `json:"q" binding:"required"`
		TopN int    `json:"top_n"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整（q 必填）")
		return
	}
	items, err := h.svc.DatasetQuery.SearchFTS(c.Request.Context(), c.Param("id"), req.Q, req.TopN)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"items": items})
}
