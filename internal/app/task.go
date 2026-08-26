package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// taskInputPayload 任务输入（task.Input 的 JSON 结构，按类型可选填充）。
type taskInputPayload struct {
	FileName            string         `json:"file_name"`
	OriginalFilePath    string         `json:"original_file_path"` // 解析步骤：上传暂存；分析后：原文存档
	ParsedText          string         `json:"parsed_text"`
	SpecialRequirements string         `json:"special_requirements"`
	DatasetName         string         `json:"dataset_name"`         // 旧版兼容：生成数据集步骤的命名
	DatasetTarget       *DatasetTarget `json:"dataset_target,omitempty"` // 写入声明：模式 + 目标数据集
}

// TaskManager 任务门面：CRUD + 人工门操作 + 生命周期触发（httpgin 的唯一入口）。
// 执行全部脱离 HTTP：步骤在独立 goroutine 中运行（spawn），进度先落库后经 Broker
// 扇出；SSE 端点订阅 Broker 实现可重接（断开只退订，任务照跑）。
type TaskManager struct {
	tasks         port.TaskRepo
	parse         parseStepRunner
	analyze       analyzeStepRunner
	datasets      port.DatasetRepo
	datasetWriter datasetStepRunner
	broker        *Broker

	mu      sync.Mutex
	running map[string]*runEntry // taskID → 运行登记（单写者约束：每任务同时只有一个步骤 goroutine）
}

// NewTaskManager 构造任务管理器。
func NewTaskManager(
	tasks port.TaskRepo,
	parse parseStepRunner,
	analyze analyzeStepRunner,
	datasets port.DatasetRepo,
	datasetWriter datasetStepRunner,
) *TaskManager {
	return &TaskManager{
		tasks: tasks, parse: parse, analyze: analyze,
		datasets: datasets, datasetWriter: datasetWriter,
		broker:  NewBroker(),
		running: make(map[string]*runEntry),
	}
}

// Subscribe 订阅任务事件流（透传 Broker；调用方负责 unsub）。
func (m *TaskManager) Subscribe(taskID string) (<-chan Event, func()) {
	return m.broker.Subscribe(taskID)
}

/* ---- 创建 / 查询 / 编辑 ---- */

// Create 创建任务：从工作流注册表取定义，快照进 tasks.workflow 并按定义播种步骤。
// 任务自描述（步骤链 + 依赖声明），不受定义后续演进影响。
func (m *TaskManager) Create(ctx context.Context, typ, title string) (*model.Task, error) {
	w, ok := WorkflowOf(typ)
	if !ok {
		return nil, fmt.Errorf("不支持的任务类型: %s", typ)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "未命名任务"
	}
	t := &model.Task{Type: typ, Title: title, Status: model.TaskStatusPending, Workflow: MarshalWorkflow(w)}
	if err := m.tasks.CreateTask(ctx, t); err != nil {
		return nil, err
	}
	steps := make([]model.TaskStep, 0, len(w.Steps))
	for _, s := range w.Steps {
		steps = append(steps, model.TaskStep{Seq: s.Seq, Name: s.Name, Status: model.StepStatusPending})
	}
	if err := m.tasks.CreateTaskSteps(ctx, t.ID, steps); err != nil {
		return nil, err
	}
	return t, nil
}

// Workflows 任务类型目录（创建入口展示用，httpgin 经此暴露）。
func (m *TaskManager) Workflows() []model.Workflow {
	return Workflows()
}

// Schemas 数据集 schema 目录（前端表格/筛选与任务输入选择用，httpgin 经此暴露）。
func (m *TaskManager) Schemas() []model.DatasetSchema {
	return model.Schemas()
}

// ListDatasets 数据集列表（结果集浏览）。
func (m *TaskManager) ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error) {
	return m.datasets.ListDatasets(ctx, typ, limit)
}

// GetDataset 数据集 + 条目（结果集浏览）。
func (m *TaskManager) GetDataset(ctx context.Context, id string) (*model.Dataset, []model.DatasetItem, error) {
	ds, err := m.datasets.GetDataset(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	items, err := m.datasets.ListDatasetItems(ctx, id, 500)
	if err != nil {
		return nil, nil, err
	}
	return ds, items, nil
}

// GetDatasetHeader 仅取数据集头信息（条目查询前的类型定位）。
func (m *TaskManager) GetDatasetHeader(ctx context.Context, id string) (*model.Dataset, error) {
	return m.datasets.GetDataset(ctx, id)
}

// List 任务列表（status/type 为空 = 不过滤）。
func (m *TaskManager) List(ctx context.Context, status, typ string, limit int) ([]model.Task, error) {
	return m.tasks.ListTasks(ctx, port.TaskFilter{Status: status, Type: typ, Limit: limit})
}

// Get 任务 + 步骤 + 明细（详情页与 SSE 快照的底料）。
func (m *TaskManager) Get(ctx context.Context, id string) (*model.Task, []model.TaskStep, []model.TaskItem, error) {
	task, err := m.tasks.GetTask(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	steps, err := m.tasks.GetTaskSteps(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	items, err := m.tasks.GetTaskItems(ctx, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return task, steps, items, nil
}

// Patch 编辑任务：标题 + 门内输入（解析文本/附加要求）。仅 awaiting/paused 可改
// （运行中由 goroutine 单写者持锁，避免双写竞态）。
func (m *TaskManager) Patch(ctx context.Context, id string, title, parsedText, specialReqs *string) (*model.Task, error) {
	task, err := m.tasks.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusAwaiting && task.Status != model.TaskStatusPaused {
		return nil, fmt.Errorf("任务状态 %s 不可编辑", task.Status)
	}
	in := taskInputOf(task)
	if title != nil {
		task.Title = strings.TrimSpace(*title)
	}
	if parsedText != nil {
		in.ParsedText = *parsedText
	}
	if specialReqs != nil {
		in.SpecialRequirements = *specialReqs
	}
	task.Input = marshalJSON(in)
	if err := m.tasks.UpdateTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// ReplaceItems 批量保存门内草稿（匹配导入门）。仅 awaiting 可改；已导入行保留不覆盖。
func (m *TaskManager) ReplaceItems(ctx context.Context, id string, inputs []DraftSaveInput) error {
	task, err := m.tasks.GetTask(ctx, id)
	if err != nil {
		return err
	}
	if task.Status != model.TaskStatusAwaiting || task.CurrentStep != 4 {
		return fmt.Errorf("当前状态不可编辑草稿")
	}
	items := make([]model.TaskItem, len(inputs))
	for i, in := range inputs {
		items[i] = model.TaskItem{ID: in.ID, DraftItem: in.Draft.toModel(), Status: model.ItemStatusPending}
	}
	return m.tasks.ReplaceTaskItems(ctx, id, items)
}

/* ---- 触发（fire-and-forget：元数据定位步骤 + 校验通过后 spawn） ---- */

// TriggerParse 上传解析步骤。multipart 文件已由 handler 存入 upload_dir；
// 路径写入 input（暂停续跑与失败重试的底料）。
func (m *TaskManager) TriggerParse(ctx context.Context, id, savedPath string) error {
	return m.triggerStep(ctx, id, model.StepKindParse, func(task *model.Task, _ model.WorkflowStep) error {
		in := taskInputOf(task)
		in.FileName = task.Title
		in.OriginalFilePath = savedPath
		task.Input = marshalJSON(in)
		return m.tasks.UpdateTask(ctx, task)
	})
}

// TriggerAnalyze AI 分析步骤（确认解析门通过后触发；paused 状态走检查点续跑）。
func (m *TaskManager) TriggerAnalyze(ctx context.Context, id string) error {
	return m.triggerStep(ctx, id, model.StepKindAnalyze, nil)
}

// TriggerGenerateDataset 写入数据集步骤（查重确认后触发；target 为写入声明：模式 + 目标）。
// 终态任务停留于本步骤时也可重触发（幂等写入策略下重写安全）。
func (m *TaskManager) TriggerGenerateDataset(ctx context.Context, id string, target DatasetTarget) error {
	target, err := target.Normalize()
	if err != nil {
		return err
	}
	return m.triggerStep(ctx, id, model.StepKindDataset, func(task *model.Task, _ model.WorkflowStep) error {
		in := taskInputOf(task)
		in.DatasetTarget = &target
		in.DatasetName = target.Name
		task.Input = marshalJSON(in)
		return m.tasks.UpdateTask(ctx, task)
	})
}

// PreviewDatasetWrite 落库前的写入预览（生成数据集门展示冲突分桶；不写入）。
func (m *TaskManager) PreviewDatasetWrite(ctx context.Context, id string, target DatasetTarget) (*WritePreview, error) {
	task, _, err := m.load(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := m.tasks.GetTaskItems(ctx, id)
	if err != nil {
		return nil, err
	}
	t, schema, err := datasetWritePlanFor(task, target)
	if err != nil {
		return nil, err
	}
	values := make([]map[string]any, len(items))
	for i := range items {
		values[i] = draftValuesOf(items[i].DraftItem)
	}
	prepared, err := m.datasetWriter.Prepare(ctx, schema, t, task.ID, values)
	if err != nil {
		return nil, err
	}
	pv := prepared.Preview()
	return &pv, nil
}

/* ---- 生命周期 ---- */

// Pause 暂停运行中的任务：取消工作 ctx → 等步骤 goroutine 收尾（已落检查点）→ 重读 DB 定夺。
// 竞态处理：取消落地前工作可能已自然完成——此时 DB 已是终态，报错提示而非覆盖。
func (m *TaskManager) Pause(ctx context.Context, id string) (*model.Task, error) {
	m.mu.Lock()
	entry, ok := m.running[id]
	if ok {
		entry.cancel()
	}
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("任务未在运行")
	}
	<-entry.done
	task, _, err := m.load(ctx, id)
	if err != nil {
		return nil, err
	}
	switch task.Status {
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusAwaiting:
		return nil, fmt.Errorf("任务已完成，无法暂停")
	}
	return task, nil
}

// Resume 继续暂停的任务：按暂停步骤的工作流定义（kind）重新触发——
// analyze 走 AgentContext 检查点续跑，import 跳过已导入条目，parse/sync 幂等重跑。
// 入参校验（源文件/目标项目）在对应执行器内完成。
func (m *TaskManager) Resume(ctx context.Context, id string) (*model.Task, error) {
	task, steps, err := m.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusPaused {
		return nil, fmt.Errorf("仅暂停的任务可继续（当前 %s）", task.Status)
	}
	pausedSeq := 0
	for _, s := range steps {
		if s.Status == model.StepStatusPaused {
			pausedSeq = s.Seq
			break
		}
	}
	var def model.WorkflowStep
	for _, d := range ParseWorkflow(task).Steps {
		if d.Seq == pausedSeq {
			def = d
			break
		}
	}
	if def.Seq == 0 {
		return nil, fmt.Errorf("无暂停中的步骤可继续")
	}
	return task, m.spawn(id, func(workCtx, pc context.Context) {
		m.runStepKind(workCtx, pc, task, steps, def)
	})
}

// Complete 手动完成等待确认的任务（终态；当前门步骤标记为已过）。
func (m *TaskManager) Complete(ctx context.Context, id string) (*model.Task, error) {
	task, steps, err := m.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if task.Status != model.TaskStatusAwaiting {
		return nil, fmt.Errorf("仅等待确认的任务可手动完成（当前 %s）", task.Status)
	}
	for i := range steps {
		if steps[i].Seq == task.CurrentStep {
			steps[i].Status = model.StepStatusSucceeded
			steps[i].Detail = "人工完成"
			steps[i].EndedAt = time.Now()
			m.saveStep(ctx, &steps[i])
		}
	}
	task.Status = model.TaskStatusSucceeded
	task.FinishedAt = time.Now()
	task.ErrorMessage = ""
	if err := m.tasks.UpdateTask(ctx, task); err != nil {
		return nil, err
	}
	m.broker.Publish(task.ID, Event{Type: "task", TaskID: task.ID, Data: task})
	return task, nil
}

// Recover 启动恢复：把服务重启前卡在 running 的任务/步骤标为 paused（可手动继续）。
func (m *TaskManager) Recover(ctx context.Context) error {
	return m.tasks.RecoverStuck(ctx)
}

/* ---- 私有 ---- */

func (m *TaskManager) load(ctx context.Context, id string) (*model.Task, []model.TaskStep, error) {
	task, err := m.tasks.GetTask(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	steps, err := m.tasks.GetTaskSteps(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if len(steps) == 0 {
		return nil, nil, fmt.Errorf("任务步骤缺失")
	}
	return task, steps, nil
}
