package httpgin

import (
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

func workflowError(c *gin.Context, err error) {
	c.JSON(appworkflow.ErrorStatus(err), gin.H{"success": false, "error": gin.H{
		"code": appworkflow.ErrorCode(err), "message": err.Error(),
	}})
}
