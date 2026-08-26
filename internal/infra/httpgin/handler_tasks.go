package httpgin

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// createTask POST /api/tasks {type, title} → 创建任务并播种步骤。
func (h *handlers) createTask(c *gin.Context) {
	var req struct {
		Type  string `json:"type" binding:"required"`
		Title string `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	task, err := h.svc.Tasks.Create(c.Request.Context(), req.Type, req.Title)
	if err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"task": task})
}

// listTasks GET /api/tasks?status=&type=&limit= → 任务列表。
func (h *handlers) listTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	tasks, err := h.svc.Tasks.List(c.Request.Context(), c.Query("status"), c.Query("type"), limit)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"tasks": tasks})
}

// listWorkflows GET /api/workflows → 任务类型目录（工作流元数据：步骤链 + 依赖声明）。
func (h *handlers) listWorkflows(c *gin.Context) {
	ok(c, gin.H{"workflows": h.svc.Tasks.Workflows()})
}

// getTask GET /api/tasks/:id → 任务 + 步骤 + 明细。
func (h *handlers) getTask(c *gin.Context) {
	task, steps, items, err := h.svc.Tasks.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 404, "任务不存在")
		return
	}
	ok(c, gin.H{"task": task, "steps": steps, "items": items})
}

// patchTask PATCH /api/tasks/:id {title?, parsed_text?, special_requirements?} → 编辑任务。
func (h *handlers) patchTask(c *gin.Context) {
	var req struct {
		Title               *string `json:"title"`
		ParsedText          *string `json:"parsed_text"`
		SpecialRequirements *string `json:"special_requirements"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	task, err := h.svc.Tasks.Patch(c.Request.Context(), c.Param("id"),
		req.Title, req.ParsedText, req.SpecialRequirements)
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task": task})
}

// taskItems POST /api/tasks/:id/items {items:[{id?, draft}]} → 批量保存门内草稿。
func (h *handlers) taskItems(c *gin.Context) {
	var req struct {
		Items []app.DraftSaveInput `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	if err := h.svc.Tasks.ReplaceItems(c.Request.Context(), c.Param("id"), req.Items); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}

// taskParse POST /api/tasks/:id/parse（multipart file）→ fire-and-forget 上传解析步骤。
// 文件存入 upload_dir（解析完成由 Runner 清理），立即返回，进度走 /events。
func (h *handlers) taskParse(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		fail(c, 400, "请上传文件")
		return
	}
	if h.svc.MaxFileMB > 0 && fileHeader.Size > h.svc.MaxFileMB<<20 {
		fail(c, 400, fmt.Sprintf("文件超过大小限制 %dMB", h.svc.MaxFileMB))
		return
	}
	fileName := filepath.Base(fileHeader.Filename)
	if err := os.MkdirAll(h.svc.UploadDir, 0o755); err != nil {
		fail(c, 500, "创建上传目录失败: "+err.Error())
		return
	}
	savedPath := filepath.Join(h.svc.UploadDir, fmt.Sprintf("%d-%s", time.Now().UnixMilli(), fileName))
	if err := c.SaveUploadedFile(fileHeader, savedPath); err != nil {
		fail(c, 500, "保存上传文件失败: "+err.Error())
		return
	}
	if err := h.svc.Tasks.TriggerParse(c.Request.Context(), c.Param("id"), savedPath); err != nil {
		_ = os.Remove(savedPath)
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task_id": c.Param("id")})
}

// taskAnalyze POST /api/tasks/:id/analyze → fire-and-forget AI 分析步骤。
func (h *handlers) taskAnalyze(c *gin.Context) {
	var req struct {
		SpecialRequirements string `json:"special_requirements"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.svc.Tasks.TriggerAnalyze(c.Request.Context(), c.Param("id")); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task_id": c.Param("id")})
}

// taskGenerateDataset POST /api/tasks/:id/dataset {mode, dataset_id?, dataset_name?}
// → fire-and-forget 写入数据集步骤（写入声明见 app.DatasetTarget）。
func (h *handlers) taskGenerateDataset(c *gin.Context) {
	var req app.DatasetTarget
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	if err := h.svc.Tasks.TriggerGenerateDataset(c.Request.Context(), c.Param("id"), req); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task_id": c.Param("id")})
}

// taskDatasetPreview POST /api/tasks/:id/dataset/preview {mode, dataset_id?, dataset_name?}
// → 写入预览：新增/更新/无变化/非法分桶（不落库）。
func (h *handlers) taskDatasetPreview(c *gin.Context) {
	var req app.DatasetTarget
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	preview, err := h.svc.Tasks.PreviewDatasetWrite(c.Request.Context(), c.Param("id"), req)
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"preview": preview})
}

// listDatasetSchemas GET /api/datasets/schemas → schema 目录（表格列/筛选器驱动）。
func (h *handlers) listDatasetSchemas(c *gin.Context) {
	ok(c, gin.H{"schemas": h.svc.Tasks.Schemas()})
}

// listDatasets GET /api/datasets?type=&limit= → 数据集列表（任务产出的结果集浏览）。
func (h *handlers) listDatasets(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	datasets, err := h.svc.Tasks.ListDatasets(c.Request.Context(), c.Query("type"), limit)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, gin.H{"datasets": datasets})
}

// getDataset GET /api/datasets/:id → 数据集 + 条目。
func (h *handlers) getDataset(c *gin.Context) {
	dataset, items, err := h.svc.Tasks.GetDataset(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 404, "数据集不存在")
		return
	}
	ok(c, gin.H{"dataset": dataset, "items": items})
}

// pauseTask POST /api/tasks/:id/pause → 暂停运行中的任务。
func (h *handlers) pauseTask(c *gin.Context) {
	task, err := h.svc.Tasks.Pause(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task": task})
}

// resumeTask POST /api/tasks/:id/resume → 继续暂停的任务。
func (h *handlers) resumeTask(c *gin.Context) {
	task, err := h.svc.Tasks.Resume(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task": task})
}

// completeTask POST /api/tasks/:id/complete → 手动完成等待确认的任务（终态）。
func (h *handlers) completeTask(c *gin.Context) {
	task, err := h.svc.Tasks.Complete(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"task": task})
}

// answerDialog POST /api/tasks/:id/dialog {call_id, answer} → 人工回答 agent 的提问
// （ask_human 工具阻塞等待的出口；无等待中的问题或 call_id 不匹配返回 409）。
func (h *handlers) answerDialog(c *gin.Context) {
	var req struct {
		CallID string `json:"call_id" binding:"required"`
		Answer string `json:"answer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数不完整")
		return
	}
	if err := h.svc.Tasks.AnswerDialog(c.Param("id"), req.CallID, req.Answer); err != nil {
		fail(c, 409, err.Error())
		return
	}
	ok(c, gin.H{"ok": true})
}

// taskEvents POST /api/tasks/:id/events → SSE：先订阅再回放快照，实时事件增量 + 5s 心跳。
// 断开只退订（任务照跑）；重连重收快照——切页/刷新后进度不丢。
func (h *handlers) taskEvents(c *gin.Context) {
	taskID := c.Param("id")
	task, steps, items, err := h.svc.Tasks.Get(c.Request.Context(), taskID)
	if err != nil {
		fail(c, 404, "任务不存在")
		return
	}
	ch, unsub := h.svc.Tasks.Subscribe(taskID)
	defer unsub()

	startSSE(c)
	// 事件形状统一（与实时事件一致）：data 键包裹，前端统一 payload = data?.data 解包。
	// dialog：当前等待人工回答的问题（阻塞事件，刷新/重连必经快照恢复弹窗）。
	sendEvent(c, "snapshot", gin.H{"data": gin.H{
		"task": task, "steps": steps, "items": items,
		"dialog": h.svc.Tasks.PendingDialog(taskID),
	}})

	hb := newHeartbeat(5*timeSecond, func() {
		if clientGone(c) {
			return
		}
		sendEvent(c, "ping", gin.H{"ts": nowMillis()})
	})
	hb.Start()
	defer hb.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case ev := <-ch:
			if clientGone(c) {
				return
			}
			sendEvent(c, ev.Type, gin.H{"task_id": ev.TaskID, "data": ev.Data})
		}
	}
}
