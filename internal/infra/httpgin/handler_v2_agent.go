package httpgin

import (
	"context"
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
	events := make(chan appplatformagent.StreamEvent, 64)
	finished := make(chan error, 1)
	clientDone := make(chan struct{})
	defer close(clientDone)

	sessionID := c.Param("id")
	runCtx := context.WithoutCancel(c.Request.Context())
	go func() {
		_, runErr := h.svc.V2Agent.RunMessage(runCtx, sessionID, input.Message, func(event appplatformagent.StreamEvent) {
			select {
			case events <- event:
			case <-clientDone:
			}
		})
		finished <- runErr
	}()

	select {
	case event := <-events:
		startSSE(c)
		sendEvent(c, event.Type, event)
	case err := <-finished:
		handleAgentRunError(c, err)
		return
	case <-c.Request.Context().Done():
		return
	}

	for {
		select {
		case event := <-events:
			sendEvent(c, event.Type, event)
		case err := <-finished:
			if err != nil {
				sendEvent(c, "error", gin.H{"error": err.Error()})
			}
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (h *handlers) v2StopAgentMessage(c *gin.Context) {
	stopped := h.svc.V2Agent.StopMessage(c.Param("id"))
	ok(c, gin.H{"stopped": stopped})
}

func (h *handlers) v2GetAgentConfig(c *gin.Context) {
	config, err := h.svc.V2Agent.GetConfig(c.Request.Context(), c.Query("workspace_id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, gin.H{"config": config})
}

func (h *handlers) v2CreateAgentSkill(c *gin.Context) {
	var input struct {
		WorkspaceID string `json:"workspace_id,omitempty"`
		appplatformagent.CreateSkillInput
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "Skill 参数非法")
		return
	}
	skill, err := h.svc.V2Agent.CreateSkill(c.Request.Context(), input.WorkspaceID, input.CreateSkillInput)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"skill": skill}})
}

func (h *handlers) v2SetAgentSkillEnabled(c *gin.Context) {
	var input struct {
		WorkspaceID string `json:"workspace_id,omitempty"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		fail(c, http.StatusBadRequest, "enabled 必须是布尔值")
		return
	}
	if err := h.svc.V2Agent.SetSkillEnabled(c.Request.Context(), input.WorkspaceID,
		c.Param("id"), *input.Enabled); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"enabled": *input.Enabled})
}

func (h *handlers) v2SetAgentToolEnabled(c *gin.Context) {
	var input struct {
		WorkspaceID string `json:"workspace_id,omitempty"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil || input.Enabled == nil {
		fail(c, http.StatusBadRequest, "enabled 必须是布尔值")
		return
	}
	if err := h.svc.V2Agent.SetToolEnabled(c.Request.Context(), input.WorkspaceID,
		c.Param("name"), *input.Enabled); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	ok(c, gin.H{"enabled": *input.Enabled})
}

func handleAgentRunError(c *gin.Context, err error) {
	if errors.Is(err, appplatformagent.ErrSessionRunning) {
		fail(c, http.StatusConflict, "这个会话正在回答，请等待完成或停止当前回答")
		return
	}
	if err == nil {
		fail(c, http.StatusBadRequest, "回答未能启动")
		return
	}
	fail(c, http.StatusBadRequest, err.Error())
}
