package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 步骤执行器小接口（既有 app 服务结构满足；测试注入假实现） ---- */

type parseStepRunner interface {
	Run(ctx context.Context, filename, filePath string, onProgress func(ParseProgress)) (string, error)
}

type analyzeStepRunner interface {
	Run(ctx context.Context, in AnalyzeInput, onProgress func(AnalyzeProgress),
		onToken func(AnalyzeDelta), onTool func(AnalyzeToolEvent)) (*AnalyzeOutcome, error)
	// Resume 从已落库会话（检查点）续跑分析（special 供降级单发重建 prompt）。
	Resume(ctx context.Context, cc *port.Context, in AnalyzeInput,
		onProgress func(AnalyzeProgress), onToken func(AnalyzeDelta), onTool func(AnalyzeToolEvent)) (*AnalyzeOutcome, error)
}

type datasetStepRunner interface {
	// Prepare 预览写入效果（分桶），Write 执行写入（两阶段共享分桶）。
	Prepare(ctx context.Context, schema model.DatasetSchema, target DatasetTarget,
		taskID string, values []map[string]any) (*PreparedWrite, error)
	Write(ctx context.Context, datasetID, taskID string, prepared *PreparedWrite,
		report func(DatasetWriteProgress)) (WriteStats, error)
}

/* ---- 运行登记 ---- */

type runEntry struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// spawn 以独立可取消 ctx 启动步骤 goroutine（脱离 HTTP 请求生命周期）。
// persistCtx 用 WithoutCancel 派生：暂停/取消后收尾落库不受工作 ctx 取消影响。
func (m *TaskManager) spawn(id string, fn func(workCtx, persistCtx context.Context)) error {
	workCtx, cancel := context.WithCancel(context.Background())
	entry := &runEntry{cancel: cancel, done: make(chan struct{})}

	m.mu.Lock()
	if _, ok := m.running[id]; ok {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("任务正在运行")
	}
	m.running[id] = entry
	m.mu.Unlock()

	persistCtx := context.WithoutCancel(workCtx)
	go func() {
		defer m.finishRun(id, entry)
		fn(workCtx, persistCtx)
	}()
	return nil
}

func (m *TaskManager) finishRun(id string, entry *runEntry) {
	m.mu.Lock()
	if cur, ok := m.running[id]; ok && cur == entry {
		delete(m.running, id)
	}
	m.mu.Unlock()
	close(entry.done)
}

/* ---- 元数据驱动分发 ---- */

// triggerStep 通用步骤触发：按 kind 定位工作流定义 → 状态校验 → 准备入参 → spawn。
// 新增任务类型只需工作流定义 + 执行器；分发不再感知具体类型。
func (m *TaskManager) triggerStep(ctx context.Context, id string, kind model.StepKind,
	prepare func(task *model.Task, def model.WorkflowStep) error) error {
	task, steps, err := m.load(ctx, id)
	if err != nil {
		return err
	}
	defs := ParseWorkflow(task).Steps
	def, ok := stepDefByKind(defs, kind)
	if !ok {
		return fmt.Errorf("任务类型 %s 不支持该步骤", task.Type)
	}
	if !canTriggerStep(task, def) {
		return fmt.Errorf("任务状态 %s 不能开始「%s」", task.Status, def.Name)
	}
	if prepare != nil {
		if err := prepare(task, def); err != nil {
			return err
		}
	}
	// 同步预置任务级状态（fire-and-forget 竞态根除）：202 返回前落库 + 发布事件，
	// 客户端任意时刻的 GET 都拿到 running + 目标步骤（goroutine 内 beginStep 幂等重申）。
	prevStatus, prevStep := task.Status, task.CurrentStep
	task.Status = model.TaskStatusRunning
	task.CurrentStep = def.Seq
	task.ErrorMessage = ""
	task.FinishedAt = time.Time{} // 终态重写场景：重新进入执行态
	if err := m.tasks.UpdateTask(ctx, task); err != nil {
		return err
	}
	m.broker.Publish(task.ID, Event{Type: "task", TaskID: task.ID, Data: task})
	if err := m.spawn(id, func(workCtx, pc context.Context) {
		m.runStepKind(workCtx, pc, task, steps, def)
	}); err != nil {
		// 预置被拒（如并发触发）：恢复原状态
		task.Status, task.CurrentStep = prevStatus, prevStep
		_ = m.tasks.UpdateTask(ctx, task)
		return err
	}
	return nil
}

// canTriggerStep 目标步骤可触发判定（元数据驱动，不感知类型）：
// pending 且为首步；或任务正停留在该步骤（awaiting 门内 / paused 中断）；
// 或任务停留在前一门步骤（awaiting 于 human 门，触发下一机器步骤，如分析）；
// 或任务已终态成功且停留于数据集步骤（幂等写入策略下可重写数据集）。
func canTriggerStep(task *model.Task, def model.WorkflowStep) bool {
	switch {
	case task.Status == model.TaskStatusPending:
		return def.Seq == 1
	case task.Status == model.TaskStatusAwaiting:
		return task.CurrentStep == def.Seq || task.CurrentStep == def.Seq-1
	case task.Status == model.TaskStatusPaused:
		return task.CurrentStep == def.Seq
	case task.Status == model.TaskStatusSucceeded:
		return def.Kind == model.StepKindDataset && task.CurrentStep == def.Seq
	}
	return false
}

// runStepKind 按步骤种类分发执行（human 为纯人工门，无执行器）。
func (m *TaskManager) runStepKind(workCtx, pc context.Context, task *model.Task, steps []model.TaskStep, def model.WorkflowStep) {
	switch def.Kind {
	case model.StepKindParse:
		m.execParseStep(workCtx, pc, task, steps, def)
	case model.StepKindAnalyze:
		m.execAnalyzeStep(workCtx, pc, task, steps, def)
	case model.StepKindDataset:
		m.execDatasetStep(workCtx, pc, task, steps, def)
	default:
		// human 或未注册种类：仅人工操作，无执行
	}
}

/* ---- 元数据小工具 ---- */

func stepDefByKind(defs []model.WorkflowStep, kind model.StepKind) (model.WorkflowStep, bool) {
	for _, d := range defs {
		if d.Kind == kind {
			return d, true
		}
	}
	return model.WorkflowStep{}, false
}

func nextStepDef(defs []model.WorkflowStep, curSeq int) *model.WorkflowStep {
	for _, d := range defs {
		if d.Seq == curSeq+1 {
			return &d
		}
	}
	return nil
}

func stepBySeq(steps []model.TaskStep, seq int) *model.TaskStep {
	for i := range steps {
		if steps[i].Seq == seq {
			return &steps[i]
		}
	}
	return nil
}

/* ---- 状态迁移辅助（全部走 persistCtx：先落库后发布） ---- */

// beginStep 步骤开始：running + 任务 running（started_at/current_step 初始化）。
func (m *TaskManager) beginStep(pc context.Context, task *model.Task, step *model.TaskStep, detail string) {
	now := time.Now()
	step.Status = model.StepStatusRunning
	step.Detail = detail
	if step.StartedAt.IsZero() {
		step.StartedAt = now
	}
	task.Status = model.TaskStatusRunning
	task.CurrentStep = step.Seq
	if task.StartedAt.IsZero() {
		task.StartedAt = now
	}
	m.saveStep(pc, step)
	m.saveTask(pc, task)
}

// finishStep 步骤成功收束（ended_at 落库）。
func (m *TaskManager) finishStep(pc context.Context, task *model.Task, step *model.TaskStep, detail string) {
	step.Status = model.StepStatusSucceeded
	step.Detail = detail
	if step.EndedAt.IsZero() {
		step.EndedAt = time.Now()
	}
	m.saveStep(pc, step)
}

// failStep 标记步骤失败（时间线展示用；ended_at 落库）。
func (m *TaskManager) failStep(pc context.Context, step *model.TaskStep, detail string) {
	step.Status = model.StepStatusFailed
	step.Detail = detail
	step.EndedAt = time.Now()
	m.saveStep(pc, step)
}

// advanceGate 步骤成功后的元数据驱动推进：下一步骤进入 awaiting（人工门）；
// 无后续步骤 → 任务终态 succeeded。parse/analyze 等机器步骤共用。
func (m *TaskManager) advanceGate(pc context.Context, task *model.Task, steps []model.TaskStep,
	defs []model.WorkflowStep, curSeq int, nextDetail string) {
	if next := nextStepDef(defs, curSeq); next != nil {
		m.gateAwaiting(pc, task, stepBySeq(steps, next.Seq), nextDetail)
		return
	}
	task.Status = model.TaskStatusSucceeded
	task.FinishedAt = time.Now()
	task.ErrorMessage = ""
	m.saveTask(pc, task)
}

// enterGate 任务进入人工门等待操作（gateStep 的步骤状态由调用方先行设置：
// failed=失败可重试 / awaiting=阶段完成等待确认）；任务停留于 gateStep。
func (m *TaskManager) enterGate(pc context.Context, task *model.Task, gateStep *model.TaskStep, detail, errMsg string) {
	gateStep.Detail = detail
	m.saveStep(pc, gateStep)
	task.Status = model.TaskStatusAwaiting
	task.CurrentStep = gateStep.Seq
	task.ErrorMessage = errMsg
	m.saveTask(pc, task)
}

// gateAwaiting 阶段正常完成后推进到下一人工门（gateStep 置为 awaiting）。
func (m *TaskManager) gateAwaiting(pc context.Context, task *model.Task, gateStep *model.TaskStep, detail string) {
	gateStep.Status = model.StepStatusAwaiting
	m.enterGate(pc, task, gateStep, detail, "")
}

// pauseTask 步骤与任务同时暂停（用户暂停/服务重启中断的落库形态）。
func (m *TaskManager) pauseTask(pc context.Context, task *model.Task, step *model.TaskStep, detail string) {
	step.Status = model.StepStatusPaused
	step.Detail = detail
	if step.EndedAt.IsZero() {
		step.EndedAt = time.Now()
	}
	m.saveStep(pc, step)
	task.Status = model.TaskStatusPaused
	m.saveTask(pc, task)
}

// failTask 步骤失败 + 任务终态失败（无人工门的步骤用；有门的步骤失败走 enterGate 重试）。
func (m *TaskManager) failTask(pc context.Context, task *model.Task, step *model.TaskStep, errMsg string) {
	step.Status = model.StepStatusFailed
	step.Detail = errMsg
	step.EndedAt = time.Now()
	m.saveStep(pc, step)
	task.Status = model.TaskStatusFailed
	task.ErrorMessage = errMsg
	task.FinishedAt = time.Now()
	m.saveTask(pc, task)
}

func (m *TaskManager) saveStep(pc context.Context, step *model.TaskStep) {
	if err := m.tasks.UpdateTaskStep(pc, step); err != nil {
		return
	}
	m.broker.Publish(step.TaskID, Event{Type: "step", TaskID: step.TaskID, Data: step})
}

func (m *TaskManager) saveTask(pc context.Context, task *model.Task) {
	if err := m.tasks.UpdateTask(pc, task); err != nil {
		return
	}
	m.broker.Publish(task.ID, Event{Type: "task", TaskID: task.ID, Data: task})
}

func (m *TaskManager) publishItems(pc context.Context, taskID string) {
	items, err := m.tasks.GetTaskItems(pc, taskID)
	if err != nil {
		return
	}
	m.broker.Publish(taskID, Event{Type: "items", TaskID: taskID, Data: items})
}

func (m *TaskManager) publishError(taskID, message string) {
	m.broker.Publish(taskID, Event{Type: "error", TaskID: taskID, Data: map[string]any{"message": message}})
}

/* ---- kind 执行器 ---- */

// execParseStep 上传解析：解析成功 → 按元数据推进到确认解析门；取消 → 暂停（原文件保留可续跑）。
func (m *TaskManager) execParseStep(workCtx, pc context.Context, task *model.Task, steps []model.TaskStep, def model.WorkflowStep) {
	step := stepBySeq(steps, def.Seq)
	in := taskInputOf(task)
	detail := "开始解析文档…"
	if strings.EqualFold(filepath.Ext(task.Title), ".pdf") {
		detail = "PDF 已上传，正在提交 MinerU 云端解析（表格/水印处理）…"
	}
	m.beginStep(pc, task, step, detail)

	text, err := m.parse.Run(workCtx, task.Title, in.OriginalFilePath, func(p ParseProgress) {
		step.Detail = p.Message
		m.saveStep(pc, step)
	})
	if err != nil {
		if workCtx.Err() != nil {
			m.pauseTask(pc, task, step, "解析已暂停")
			return
		}
		m.failStep(pc, step, "解析失败: "+err.Error())
		m.enterGate(pc, task, step, "解析失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}

	// 解析成功：写入 input，进入确认解析门；上传暂存文件使命完成即清理
	in.FileName = task.Title
	in.ParsedText = text
	task.Input = marshalJSON(in)
	if in.OriginalFilePath != "" {
		_ = os.Remove(in.OriginalFilePath)
	}
	m.finishStep(pc, task, step, fmt.Sprintf("解析完成，全文 %d 字", len([]rune(text))))
	m.advanceGate(pc, task, steps, ParseWorkflow(task).Steps, def.Seq, "请确认解析结果，确认后开始 AI 分析")
}

// execAnalyzeStep AI 分析：agent loop 会话检查点落库（暂停续跑）；成功 → 匹配导入门。
func (m *TaskManager) execAnalyzeStep(workCtx, pc context.Context, task *model.Task, steps []model.TaskStep, def model.WorkflowStep) {
	step := stepBySeq(steps, def.Seq)
	gateStep := stepBySeq(steps, def.Seq-1) // 前一门步骤（确认解析）：失败时回到此处可重试

	// 确认解析门通过
	gateStep.Status = model.StepStatusSucceeded
	gateStep.Detail = "解析确认通过"
	gateStep.EndedAt = time.Now()
	m.saveStep(pc, gateStep)

	in := taskInputOf(task)
	if strings.TrimSpace(in.ParsedText) == "" {
		m.failStep(pc, step, "缺少待分析文本")
		m.enterGate(pc, task, gateStep, "缺少待分析文本（请先完成解析）", "缺少待分析文本")
		m.publishError(task.ID, "缺少待分析文本")
		return
	}

	// 上次失败重试：清空会话检查点，保证全新分析
	var cc *port.Context
	if task.AgentContext != "" {
		_ = json.Unmarshal([]byte(task.AgentContext), &cc)
	}

	var trace []AnalyzeToolEvent
	traceStep := func() {
		if data, err := json.Marshal(trace); err == nil {
			step.Data = string(data)
			m.saveStep(pc, step)
		}
	}
	onTool := func(ev AnalyzeToolEvent) {
		trace = append(trace, ev)
		if ev.Phase == "end" {
			traceStep()
		}
		m.broker.Publish(task.ID, Event{Type: "tool_trace", TaskID: task.ID, Data: ev})
	}
	// token 事件节流合并：逐 token 一帧会打爆 broker 64 缓冲（慢消费者下丢帧，
	// 表现为推理/正文面板空白），按 150ms 窗口合并成批帧，频率降到每秒 ~7 帧。
	tokCtx, tokCancel := context.WithCancel(workCtx)
	defer tokCancel()
	var (
		tokMu    sync.Mutex
		tokBuf   strings.Builder
		tokPhase string
	)
	flushTokens := func() {
		tokMu.Lock()
		defer tokMu.Unlock()
		if tokBuf.Len() == 0 {
			return
		}
		m.broker.Publish(task.ID, Event{Type: "token", TaskID: task.ID,
			Data: map[string]any{"delta": tokBuf.String(), "phase": tokPhase}})
		tokBuf.Reset()
	}
	go func() {
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-tokCtx.Done():
				return
			case <-ticker.C:
				flushTokens()
			}
		}
	}()
	defer flushTokens() // 分析结束（含暂停/失败路径）兜底清空积压
	onToken := func(d AnalyzeDelta) {
		tokMu.Lock()
		// 阶段切换：旧段整帧发出，新段从当前增量起积累
		if tokPhase != "" && d.Phase != tokPhase {
			prev, prevPhase := tokBuf.String(), tokPhase
			tokBuf.Reset()
			tokPhase = d.Phase
			tokBuf.WriteString(d.Text)
			tokMu.Unlock()
			m.broker.Publish(task.ID, Event{Type: "token", TaskID: task.ID,
				Data: map[string]any{"delta": prev, "phase": prevPhase}})
			return
		}
		tokPhase = d.Phase
		tokBuf.WriteString(d.Text)
		tokMu.Unlock()
	}
	onProgress := func(p AnalyzeProgress) {
		step.Detail = p.Message
		m.broker.Publish(task.ID, Event{Type: "progress", TaskID: task.ID, Data: map[string]any{"stage": p.Stage, "message": p.Message}})
		m.saveStep(pc, step)
	}

	m.beginStep(pc, task, step, "AI 正在拆解需求功能点…")

	var out *AnalyzeOutcome
	var err error
	ain := AnalyzeInput{
		TaskID: task.ID, TaskType: task.Type, FileName: task.Title,
		Text: in.ParsedText, Special: in.SpecialRequirements,
		Dialog: m.dialogs,
	}
	defer m.dialogs.Clear(task.ID) // 人工交互兜底清理（Ask 出口自清，通常无事可做）
	if cc != nil {
		out, err = m.analyze.Resume(workCtx, cc, ain, onProgress, onToken, onTool)
	} else {
		out, err = m.analyze.Run(workCtx, ain, onProgress, onToken, onTool)
	}
	if err != nil {
		traceStep()
		if workCtx.Err() != nil {
			// 暂停检查点：保留已积累会话供续跑
			if out != nil && out.AgentContext != "" {
				task.AgentContext = out.AgentContext
			}
			m.pauseTask(pc, task, step, "分析已暂停")
			return
		}
		// 分析失败：回到前一门步骤可重试（清会话保全新分析）
		task.AgentContext = ""
		m.failStep(pc, step, "分析失败: "+err.Error())
		m.enterGate(pc, task, gateStep, "分析失败，可重新开始", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	traceStep()

	// 成功：明细落库 + 会话存档 + 按元数据推进到匹配导入门
	task.AgentContext = out.AgentContext
	if out.SourcePath != "" {
		in.OriginalFilePath = out.SourcePath
		task.Input = marshalJSON(in)
	}
	if err := m.tasks.ReplaceTaskItems(pc, task.ID, out.Items); err != nil {
		m.failStep(pc, step, "明细落库失败: "+err.Error())
		m.enterGate(pc, task, gateStep, "明细落库失败，可重新开始", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	m.finishStep(pc, task, step, fmt.Sprintf("分析完成，共 %d 个工作项", len(out.Items)))
	m.advanceGate(pc, task, steps, ParseWorkflow(task).Steps, def.Seq, "请确认草稿并命名数据集，确认后生成")
	m.publishItems(pc, task.ID)
}

// execDatasetStep 写入数据集：Prepare 分桶（预览同一逻辑）→ 仅变化条目向量化 →
// 按模式幂等写入（create 全量重建 / merge 跳过 / upsert 更新 / replace 同源覆盖）→ 发布。
// 断点续跑/失败重试复用同一 building 数据集；终态任务可换目标重写（幂等安全）。
func (m *TaskManager) execDatasetStep(workCtx, pc context.Context, task *model.Task, steps []model.TaskStep, def model.WorkflowStep) {
	step := stepBySeq(steps, def.Seq)
	m.beginStep(pc, task, step, "开始写入数据集…")

	items, err := m.tasks.GetTaskItems(pc, task.ID)
	if err != nil {
		m.failStep(pc, step, "读取草稿失败: "+err.Error())
		m.enterGate(pc, task, step, "读取草稿失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	if len(items) == 0 {
		m.failStep(pc, step, "没有可写入的条目")
		m.enterGate(pc, task, step, "没有可写入的条目（请先完成 AI 分析）", "没有可写入的条目")
		m.publishError(task.ID, "没有可写入的条目")
		return
	}

	target, schema, err := datasetWritePlan(task)
	if err != nil {
		m.failStep(pc, step, "写入声明无效: "+err.Error())
		m.enterGate(pc, task, step, "写入声明无效，可调整后重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	values := make([]map[string]any, len(items))
	for i := range items {
		values[i] = draftValuesOf(items[i].DraftItem)
	}
	prepared, err := m.datasetWriter.Prepare(pc, schema, target, task.ID, values)
	if err != nil {
		m.failStep(pc, step, "写入预检失败: "+err.Error())
		m.enterGate(pc, task, step, "写入预检失败，可调整后重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	// 全部非法（如整批缺标题）：写入无意义，直接回门
	if prepared.Preview().Insert+prepared.Preview().Update == 0 {
		msg := "没有可写入的新内容（全部跳过或非法）"
		m.failStep(pc, step, msg)
		m.enterGate(pc, task, step, msg+"，请修正草稿后重试", msg)
		m.publishError(task.ID, msg)
		return
	}

	datasetID, err := m.resolveWriteTarget(pc, task, target, schema)
	if err != nil {
		m.failStep(pc, step, "定位目标数据集失败: "+err.Error())
		m.enterGate(pc, task, step, "定位目标数据集失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}

	stats, err := m.datasetWriter.Write(workCtx, datasetID, task.ID, prepared, func(p DatasetWriteProgress) {
		step.Detail = fmt.Sprintf("已写入 %d/%d", p.Current, p.Total)
		m.broker.Publish(task.ID, Event{Type: "progress", TaskID: task.ID, Data: map[string]any{
			"current": p.Current, "total": p.Total,
		}})
		m.saveStep(pc, step)
	})
	if err != nil {
		if workCtx.Err() != nil {
			m.pauseTask(pc, task, step, "数据集写入已暂停")
			return
		}
		m.failStep(pc, step, "数据集写入失败: "+err.Error())
		m.enterGate(pc, task, step, "数据集写入失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}

	// 发布：item_count 取数据集真实条目数（merge/upsert 后不等于本次写入量）
	n, err := m.datasets.CountDatasetItemsOfDataset(pc, datasetID)
	if err != nil {
		n = int64(stats.Written)
	}
	ds, err := m.datasets.GetDataset(pc, datasetID)
	if err != nil {
		m.failStep(pc, step, "读取数据集失败: "+err.Error())
		m.enterGate(pc, task, step, "读取数据集失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	ds.Status = model.DatasetStatusReady
	ds.ItemCount = int(n)
	if err := m.datasets.UpdateDataset(pc, ds); err != nil {
		m.failStep(pc, step, "数据集发布失败: "+err.Error())
		m.enterGate(pc, task, step, "数据集发布失败，可重试", err.Error())
		m.publishError(task.ID, err.Error())
		return
	}
	// 草稿状态回写（prepared.Items 与 items 一一对应）
	for i, it := range prepared.Items {
		status, errMsg := model.ItemStatusSuccess, ""
		if it.Action == ActionInvalid {
			status, errMsg = model.ItemStatusFailed, it.InvalidMsg
		}
		_ = m.tasks.UpdateItemResult(pc, items[i].ID, status, errMsg)
	}
	pv := prepared.Preview()
	detail := fmt.Sprintf("数据集「%s」写入完成：新增 %d、更新 %d、跳过 %d（共 %d 条）",
		ds.Name, pv.Insert, pv.Update, pv.Unchanged+pv.Invalid, n)
	m.finishStep(pc, task, step, detail)
	m.advanceGate(pc, task, steps, ParseWorkflow(task).Steps, def.Seq, "")
	m.publishItems(pc, task.ID)
}

// resolveWriteTarget 定位写入目标：create 复用 building 数据集（断点续跑/失败重试）或新建；
// merge/upsert/replace 校验目标存在后直接采用。
func (m *TaskManager) resolveWriteTarget(pc context.Context, task *model.Task,
	target DatasetTarget, schema model.DatasetSchema) (string, error) {
	if target.Mode != WriteModeCreate {
		return target.DatasetID, nil
	}
	// create + 已有产出数据集：building 态复用（续跑），ready 态视为换目标重建（终态重写）
	if task.OutputDatasetID != "" {
		if ds, err := m.datasets.GetDataset(pc, task.OutputDatasetID); err == nil &&
			ds.Status == model.DatasetStatusBuilding {
			return ds.ID, nil
		}
	}
	ds := &model.Dataset{
		Type: schema.Type, Name: target.Name,
		SourceTaskID: task.ID, Status: model.DatasetStatusBuilding,
		SchemaVersion: schema.Version,
	}
	if err := m.datasets.CreateDataset(pc, ds); err != nil {
		return "", err
	}
	task.OutputDatasetID = ds.ID
	m.saveTask(pc, task)
	return ds.ID, nil
}

// datasetWritePlan 解析任务的写入声明（兼容旧 dataset_name）与产出 schema。
func datasetWritePlan(task *model.Task) (DatasetTarget, model.DatasetSchema, error) {
	in := taskInputOf(task)
	target := in.DatasetTarget
	if target == nil {
		target = &DatasetTarget{Mode: WriteModeCreate, Name: in.DatasetName}
	}
	return datasetWritePlanFor(task, *target)
}

// datasetWritePlanFor 以显式声明（预览请求/任务输入）解析写入计划。
func datasetWritePlanFor(task *model.Task, target DatasetTarget) (DatasetTarget, model.DatasetSchema, error) {
	t, err := target.Normalize()
	if err != nil {
		return t, model.DatasetSchema{}, err
	}
	dsType, ok := model.DatasetTypeOfTask(task.Type)
	if !ok {
		return t, model.DatasetSchema{}, fmt.Errorf("任务类型 %s 未注册产出数据集类型", task.Type)
	}
	schema, ok := model.SchemaOf(dsType)
	if !ok {
		return t, model.DatasetSchema{}, fmt.Errorf("数据集类型 %s 未注册 schema", dsType)
	}
	return t, schema, nil
}

/* ---- 小工具 ---- */

func taskInputOf(task *model.Task) taskInputPayload {
	var in taskInputPayload
	_ = json.Unmarshal([]byte(task.Input), &in)
	return in
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
