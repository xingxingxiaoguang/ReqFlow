package httpgin

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	appworkflow "reqflow/internal/app/workflow"
	domain "reqflow/internal/domain/workflow"
)

func (h *handlers) createWorkflowRun(c *gin.Context) {
	var request appworkflow.CreateRunRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "运行创建参数非法")
		return
	}
	snapshot, err := h.svc.WorkflowRuntime.Create(c.Request.Context(), request)
	if err != nil {
		workflowError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": snapshot})
}

func (h *handlers) listWorkflowRuns(c *gin.Context) {
	runs, err := h.svc.WorkflowRuntime.List(c.Request.Context(), c.Query("workspace_id"), 0)
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, gin.H{"runs": runs})
}

func (h *handlers) getWorkflowRun(c *gin.Context) {
	snapshot, err := h.svc.WorkflowRuntime.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, snapshot)
}

func (h *handlers) startWorkflowRun(c *gin.Context) {
	h.transitionWorkflowRun(c, h.svc.WorkflowRuntime.Start)
}

func (h *handlers) pauseWorkflowRun(c *gin.Context) {
	h.transitionWorkflowRun(c, h.svc.WorkflowRuntime.Pause)
}

func (h *handlers) resumeWorkflowRun(c *gin.Context) {
	h.transitionWorkflowRun(c, h.svc.WorkflowRuntime.Resume)
}

func (h *handlers) transitionWorkflowRun(c *gin.Context, transition func(context.Context, string) error) {
	if err := transition(c.Request.Context(), c.Param("id")); err != nil {
		workflowError(c, err)
		return
	}
	snapshot, err := h.svc.WorkflowRuntime.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, snapshot)
}

func (h *handlers) retryWorkflowNode(c *gin.Context) {
	if err := h.svc.WorkflowRuntime.Retry(c.Request.Context(), c.Param("id"), c.Param("node_id")); err != nil {
		workflowError(c, err)
		return
	}
	snapshot, err := h.svc.WorkflowRuntime.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, snapshot)
}

func (h *handlers) completeWorkflowNodeManual(c *gin.Context) {
	var request struct {
		Outputs []domain.NodeResourceBinding `json:"outputs"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		fail(c, http.StatusUnprocessableEntity, "人工产物参数非法")
		return
	}
	if err := h.svc.WorkflowRuntime.CompleteManual(c.Request.Context(), c.Param("id"), c.Param("node_id"), appworkflow.LocalActorID, request.Outputs); err != nil {
		workflowError(c, err)
		return
	}
	snapshot, err := h.svc.WorkflowRuntime.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		workflowError(c, err)
		return
	}
	ok(c, snapshot)
}
