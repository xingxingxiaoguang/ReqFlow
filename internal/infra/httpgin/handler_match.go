package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// matchProjects POST /api/match/projects {names: [项目名…]}
// → {matches: [{id,name,score,match_type,suggested_name}]}（精确前置 + 语义兜底）。
func (h *handlers) matchProjects(c *gin.Context) {
	var req struct {
		Names []string `json:"names" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "请提供项目名列表")
		return
	}
	matches, err := h.svc.Match.MatchProjects(c.Request.Context(), req.Names)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"matches": matches})
}

// checkDuplicates POST /api/match/duplicates {project_id, items: [草稿…]}
// → {results: [{index, match: {id,title,score,match_type} | null}]}。
func (h *handlers) checkDuplicates(c *gin.Context) {
	var req struct {
		ProjectID string             `json:"project_id" binding:"required"`
		Items     []app.DraftInput   `json:"items" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	results, err := h.svc.Match.CheckDuplicates(c.Request.Context(), req.ProjectID, req.Items)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"results": results})
}
