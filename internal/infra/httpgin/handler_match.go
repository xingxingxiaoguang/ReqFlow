package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// checkDuplicates POST /api/match/duplicates {items: [草稿…]}
// → {results: [{index, match: {id,dataset_id,title,score,match_type} | null}]}
// 语料 = 已有需求数据集（跨数据集查重）。
func (h *handlers) checkDuplicates(c *gin.Context) {
	var req struct {
		Items []app.DraftInput `json:"items" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	results, err := h.svc.Match.CheckDuplicates(c.Request.Context(), req.Items)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"results": results})
}
