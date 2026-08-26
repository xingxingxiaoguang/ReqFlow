package httpgin

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
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

// queryDatasetItems GET /api/datasets/:id/items?q=&f[priority]=High&f[type_id]=story|bug&limit=
// → 条目筛选（schema filterable 字段下推）+ 语义检索（q 非空且 embedding 可用）。
// f 值以 | 分隔表示 in；q 与 f 可叠加。
func (h *handlers) queryDatasetItems(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	dataset, err := h.svc.Tasks.GetDatasetHeader(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 404, "数据集不存在")
		return
	}
	q := app.DatasetQuery{
		DatasetID: c.Param("id"),
		Type:      dataset.Type,
		Text:      c.Query("q"),
		TopN:      limit,
	}
	for field, raw := range c.QueryMap("f") {
		vals := strings.Split(raw, "|")
		if len(vals) == 1 {
			q.Filters = append(q.Filters, app.FieldCondition{Field: field, Value: vals[0]})
			continue
		}
		anyVals := make([]any, len(vals))
		for i, v := range vals {
			anyVals[i] = v
		}
		q.Filters = append(q.Filters, app.FieldCondition{Field: field, Op: "in", Value: anyVals})
	}
	hits, err := h.svc.DatasetQuery.Query(c.Request.Context(), q)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"items": hits, "total": len(hits)})
}
