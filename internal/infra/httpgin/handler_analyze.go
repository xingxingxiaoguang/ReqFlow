package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// analyzeStream POST /api/analyze {text, file_name, special_requirements} → SSE。
// token 事件携带 {delta, phase}（thinking/answer 两相位）；分析思考期每 5 秒推送心跳计时。
// tool 事件（agent 模式）携带工具调用轨迹 {phase, call_id, name, args?, details?, is_error?}。
func (h *handlers) analyzeStream(c *gin.Context) {
	var req struct {
		Text               string `json:"text"`
		FileName           string `json:"file_name"`
		SpecialRequirements string `json:"special_requirements"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || trim(req.Text) == "" {
		fail(c, 400, "缺少待分析文本")
		return
	}
	if req.FileName == "" {
		req.FileName = "未命名文档"
	}

	startSSE(c)

	// 分析阶段心跳：LLM 思考期间无增量内容，推送已用时间让前端展示计时
	analyzeStart := nowMillis()
	heartbeat := newHeartbeat(5*timeSecond, func() {
		if clientGone(c) {
			return
		}
		sendEvent(c, "progress", gin.H{
			"stage": "analyzing", "status": "running",
			"message": "AI 正在拆解需求功能点…",
			"elapsedSec": int((nowMillis() - analyzeStart) / 1000),
		})
	})
	heartbeat.Start()
	defer heartbeat.Stop()

	sendEvent(c, "progress", gin.H{"stage": "analyzing", "status": "running", "message": "AI 正在拆解需求功能点…"})

	result, err := h.svc.Analyze.Run(
		c.Request.Context(), req.FileName, req.Text, req.SpecialRequirements,
		func(p app.AnalyzeProgress) {
			heartbeat.Stop() // 有任何阶段消息即停心跳
			if clientGone(c) {
				return
			}
			sendEvent(c, "progress", gin.H{"stage": p.Stage, "status": "running", "message": p.Message})
		},
		func(d app.AnalyzeDelta) {
			heartbeat.Stop() // 首 token 到达即停心跳
			if clientGone(c) {
				return
			}
			sendEvent(c, "token", gin.H{"delta": d.Text, "phase": d.Phase})
		},
		func(ev app.AnalyzeToolEvent) {
			heartbeat.Stop() // 工具执行期间有明确进度，无需心跳
			if clientGone(c) {
				return
			}
			sendEvent(c, "tool", ev)
		},
	)
	if err != nil {
		heartbeat.Stop()
		sendEvent(c, "error", gin.H{"message": err.Error()})
		return
	}

	items := make([]gin.H, len(result.Items))
	for i, it := range result.Items {
		items[i] = gin.H{
			"id": it.ID, "record_id": result.Record.ID,
			"project_name": it.ProjectName, "title": it.Title, "description": it.Description,
			"priority": it.Priority, "estimated_hours": it.EstimatedHours,
			"start_at": it.StartAt, "end_at": it.EndAt, "type_id": it.TypeID,
			"assignee_name": it.AssigneeName, "state": it.State,
			"solution_suggestion": it.SolutionSuggestion,
			"status": it.Status,
		}
	}
	sendEvent(c, "complete", gin.H{"record_id": result.Record.ID, "items": items})
}
