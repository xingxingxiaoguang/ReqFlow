package httpgin

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"

	appworkflow "reqflow/internal/app/workflow"
)

func (h *handlers) workflowCapabilities(c *gin.Context) {
	ok(c, gin.H{"capabilities": h.svc.Workflows.Capabilities()})
}

func (h *handlers) createWorkflow(c *gin.Context) {
	var request appworkflow.CreateWorkflowRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "流程创建参数非法")
		return
	}
	view, err := h.svc.Workflows.Create(c.Request.Context(), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": view})
}

func (h *handlers) listWorkflows(c *gin.Context) {
	workflows, err := h.svc.Workflows.List(c.Request.Context(), c.Query("workspace_id"), 0)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, gin.H{"workflows": workflows})
}

func (h *handlers) getWorkflow(c *gin.Context) {
	view, err := h.svc.Workflows.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) executeWorkflowCommand(c *gin.Context) {
	var request appworkflow.CommandRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "流程命令参数非法")
		return
	}
	result, err := h.svc.Workflows.ExecuteCommand(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, result)
}

func (h *handlers) validateWorkflow(c *gin.Context) {
	var request appworkflow.ValidateRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			fail(c, http.StatusUnprocessableEntity, "流程校验参数非法")
			return
		}
	}
	result, err := h.svc.Workflows.Validate(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, result)
}

func (h *handlers) createWorkflowPreview(c *gin.Context) {
	var request appworkflow.CreatePreviewRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "预览参数非法")
		return
	}
	preview, err := h.svc.WorkflowPreviews.Create(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": preview})
}

func (h *handlers) getWorkflowPreview(c *gin.Context) {
	preview, err := h.svc.WorkflowPreviews.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, preview)
}

func (h *handlers) runWorkflowAcceptance(c *gin.Context) {
	var request struct {
		PreviewID string `json:"preview_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.PreviewID == "" {
		fail(c, http.StatusUnprocessableEntity, "验收必须提供 preview_id")
		return
	}
	result, err := h.svc.WorkflowPreviews.RunAcceptance(c.Request.Context(), c.Param("id"), c.Param("case_id"), request.PreviewID)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, result)
}

func (h *handlers) publishWorkflow(c *gin.Context) {
	var request appworkflow.PublishRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "发布参数非法")
		return
	}
	revision, err := h.svc.WorkflowPublications.Publish(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": revision})
}

func (h *handlers) listWorkflowRevisions(c *gin.Context) {
	revisions, err := h.svc.WorkflowPublications.ListRevisions(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, gin.H{"revisions": revisions})
}

func (h *handlers) getWorkflowRevision(c *gin.Context) {
	revision, err := h.svc.WorkflowPublications.GetRevision(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, revision)
}

func (h *handlers) createDesignSession(c *gin.Context) {
	var request struct {
		AgentAvailable bool `json:"agent_available"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&request); err != nil {
			fail(c, http.StatusUnprocessableEntity, "设计会话参数非法")
			return
		}
	}
	view, err := h.svc.WorkflowDesign.Create(c.Request.Context(), c.Param("id"), request.AgentAvailable)
	if err != nil {
		workflowError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": view})
}

func (h *handlers) getDesignSession(c *gin.Context) {
	view, err := h.svc.WorkflowDesign.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) runDesignSession(c *gin.Context) {
	var request struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Message == "" {
		fail(c, http.StatusUnprocessableEntity, "设计消息不能为空")
		return
	}
	view, err := h.svc.WorkflowDesign.Run(c.Request.Context(), c.Param("id"), request.Message)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) answerDesignQuestion(c *gin.Context) {
	var request struct {
		Answer json.RawMessage `json:"answer"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "人工回答参数非法")
		return
	}
	view, err := h.svc.WorkflowDesign.Answer(c.Request.Context(), c.Param("id"), request.Answer, appworkflow.LocalActorID)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) acceptDesignProposal(c *gin.Context) {
	view, err := h.svc.WorkflowDesign.AcceptProposal(c.Request.Context(), c.Param("id"), c.Param("proposal_id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) rejectDesignProposal(c *gin.Context) {
	view, err := h.svc.WorkflowDesign.RejectProposal(c.Request.Context(), c.Param("id"), c.Param("proposal_id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func (h *handlers) switchDesignManual(c *gin.Context) {
	view, err := h.svc.WorkflowDesign.SwitchToManual(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, view)
}

func workflowError(c *gin.Context, err error) {
	c.JSON(appworkflow.ErrorStatus(err), gin.H{"success": false, "error": gin.H{
		"code": appworkflow.ErrorCode(err), "message": err.Error(),
	}})
}
