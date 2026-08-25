package httpgin

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// listRecords GET /api/records?limit=
func (h *handlers) listRecords(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	records, err := h.svc.Record.List(c.Request.Context(), limit)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"records": records})
}

// getRecord GET /api/records/:id → 记录 + 全部明细（前端「恢复」用）。
func (h *handlers) getRecord(c *gin.Context) {
	rec, items, err := h.svc.Record.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 404, "记录不存在")
		return
	}
	ok(c, gin.H{"record": rec, "items": items})
}

// recordSource GET /api/records/:id/source → 分析原文全文。
func (h *handlers) recordSource(c *gin.Context) {
	text, err := h.svc.Record.Source(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 404, err.Error())
		return
	}
	ok(c, gin.H{"text": text})
}
