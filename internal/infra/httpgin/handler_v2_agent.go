package httpgin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	appplatformagent "reqflow/internal/app/platformagent"
)

func (h *handlers) v2ListAgentSessions(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil {
		fail(c, http.StatusBadRequest, "limit 必须是整数")
		return
	}
	sessions, err := h.svc.V2Agent.ListSessions(c.Request.Context(), c.Query("workspace_id"), limit)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"sessions": sessions})
}

func (h *handlers) v2CreateAgentSession(c *gin.Context) {
	var input struct {
		WorkspaceID string `json:"workspace_id,omitempty"`
		Title       string `json:"title,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "会话参数非法")
		return
	}
	session, err := h.svc.V2Agent.CreateSession(c.Request.Context(), input.WorkspaceID, input.Title)
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"session": session}})
}

func (h *handlers) v2GetAgentSession(c *gin.Context) {
	session, err := h.svc.V2Agent.GetSession(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "Agent 会话不存在")
		return
	}
	ok(c, gin.H{"session": session})
}

func (h *handlers) v2RunAgentMessage(c *gin.Context) {
	var input struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "消息参数非法")
		return
	}
	streamStarted := false
	_, err := h.svc.V2Agent.RunMessage(c.Request.Context(), c.Param("id"), input.Message, func(event appplatformagent.StreamEvent) {
		if !streamStarted {
			startSSE(c)
			streamStarted = true
		}
		sendEvent(c, event.Type, event)
	})
	if err == nil {
		return
	}
	if streamStarted {
		sendEvent(c, "error", gin.H{"error": err.Error()})
		return
	}
	if errors.Is(err, appplatformagent.ErrSessionRunning) {
		fail(c, http.StatusConflict, "这个会话正在回答，请等待完成或停止当前回答")
		return
	}
	fail(c, http.StatusBadRequest, err.Error())
}
