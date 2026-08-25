package httpgin

import (
	"github.com/gin-gonic/gin"
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

// listProjects GET /api/projects 已同步项目。
func (h *handlers) listProjects(c *gin.Context) {
	projects, err := h.svc.Browse.ListProjects(c.Request.Context())
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"projects": projects})
}

// listWorkItems GET /api/work-items?project_id=&search=&limit=&offset=
func (h *handlers) listWorkItems(c *gin.Context) {
	f := browseFilterFromQuery(c)
	items, total, err := h.svc.Browse.ListWorkItems(c.Request.Context(), f)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"items": items, "total": total})
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

// testPingCode POST /api/settings/test-pingcode
func (h *handlers) testPingCode(c *gin.Context) {
	if err := h.svc.Settings.TestPingCode(c.Request.Context()); err != nil {
		fail(c, 502, err.Error())
		return
	}
	ok(c, gin.H{"message": "PingCode 连接正常"})
}
