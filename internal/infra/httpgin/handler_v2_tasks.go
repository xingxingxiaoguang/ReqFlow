package httpgin

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	apporchestrator "reqflow/internal/app/orchestrator"
)

func (h *handlers) v2CreateTaskDefinition(c *gin.Context) {
	var input apporchestrator.CreateDefinitionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "任务定义 JSON 非法或缺少必填字段")
		return
	}
	definition, err := h.svc.V2Definitions.Register(c.Request.Context(), input)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"definition": definition}})
}

func (h *handlers) v2CreateTask(c *gin.Context) {
	var input apporchestrator.CreateExecutionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "任务输入 JSON 非法")
		return
	}
	task, err := h.svc.V2Definitions.CreateExecution(c.Request.Context(), input)
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": gin.H{"task": task}})
}

func (h *handlers) v2GetTask(c *gin.Context) {
	snapshot, err := h.svc.V2Runtime.Snapshot(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "V2 任务不存在")
		return
	}
	ok(c, snapshot)
}

func (h *handlers) v2StartTask(c *gin.Context)  { h.v2Transition(c, h.svc.V2Runtime.Start) }
func (h *handlers) v2PauseTask(c *gin.Context)  { h.v2Transition(c, h.svc.V2Runtime.Pause) }
func (h *handlers) v2ResumeTask(c *gin.Context) { h.v2Transition(c, h.svc.V2Runtime.Resume) }

func (h *handlers) v2Transition(c *gin.Context, transition func(context.Context, string) error) {
	if err := transition(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	snapshot, err := h.svc.V2Runtime.Snapshot(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	ok(c, snapshot)
}

func (h *handlers) v2RetryStep(c *gin.Context) {
	if err := h.svc.V2Runtime.Retry(c.Request.Context(), c.Param("id"), c.Param("step_id")); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	h.v2GetTask(c)
}

func (h *handlers) v2ApproveStep(c *gin.Context) {
	var input apporchestrator.HumanApprovalInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, "人工审核输出 JSON 非法")
		return
	}
	if err := h.svc.V2Runtime.Approve(c.Request.Context(), c.Param("id"), c.Param("step_id"), input); err != nil {
		fail(c, http.StatusConflict, err.Error())
		return
	}
	h.v2GetTask(c)
}

// V2 SSE 以数据库快照为事实源：每秒 diff 一次，变化时推 snapshot；即使事件丢失、
// 服务切实例或客户端重连，也能恢复完整状态。心跳和快照在同一 goroutine 写响应。
func (h *handlers) v2TaskEvents(c *gin.Context) {
	taskID := c.Param("id")
	snapshot, err := h.svc.V2Runtime.Snapshot(c.Request.Context(), taskID)
	if err != nil {
		fail(c, http.StatusNotFound, "V2 任务不存在")
		return
	}
	last, _ := json.Marshal(snapshot)
	startSSE(c)
	sendEvent(c, "snapshot", gin.H{"task_id": taskID, "data": snapshot})

	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			sendEvent(c, "ping", gin.H{"ts": nowMillis()})
		case <-poll.C:
			next, err := h.svc.V2Runtime.Snapshot(c.Request.Context(), taskID)
			if err != nil {
				sendEvent(c, "error", gin.H{"task_id": taskID, "error": err.Error()})
				return
			}
			raw, _ := json.Marshal(next)
			if string(raw) == string(last) {
				continue
			}
			last = raw
			sendEvent(c, "snapshot", gin.H{"task_id": taskID, "data": next})
		}
	}
}
