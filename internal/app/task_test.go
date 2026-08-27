package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- mock TaskRepo：内存实现（镜像仓储契约，含并发安全的简单模拟） ---- */

type memTasks struct {
	mu    sync.Mutex
	tasks []model.Task // 插入序即 created_at 序（ListTasks 按此逆序）
	steps map[string][]model.TaskStep
	items map[string][]model.TaskItem
	seq   int
}

func newMemTasks() *memTasks {
	return &memTasks{steps: make(map[string][]model.TaskStep), items: make(map[string][]model.TaskItem)}
}

func (r *memTasks) CreateTask(ctx context.Context, t *model.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	t.ID = fmt.Sprintf("task-%d", r.seq)
	t.CreatedAt, t.UpdatedAt = time.Now(), time.Now()
	r.tasks = append(r.tasks, *t)
	return nil
}

func (r *memTasks) UpdateTask(ctx context.Context, t *model.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].ID == t.ID {
			t.UpdatedAt = time.Now()
			r.tasks[i] = *t
			return nil
		}
	}
	return fmt.Errorf("任务不存在")
}

func (r *memTasks) ListTasks(ctx context.Context, f port.TaskFilter) ([]model.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.Task, 0, len(r.tasks))
	for i := len(r.tasks) - 1; i >= 0; i-- {
		t := r.tasks[i]
		if f.Status != "" && t.Status != f.Status {
			continue
		}
		if f.Type != "" && t.Type != f.Type {
			continue
		}
		out = append(out, t)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = len(out)
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func (r *memTasks) CountTasks(ctx context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.tasks)), nil
}

func (r *memTasks) GetTask(ctx context.Context, id string) (*model.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].ID == id {
			t := r.tasks[i]
			return &t, nil
		}
	}
	return nil, fmt.Errorf("任务不存在")
}

func (r *memTasks) CreateTaskSteps(ctx context.Context, taskID string, steps []model.TaskStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range steps {
		r.seq++
		steps[i].ID = fmt.Sprintf("step-%d", r.seq)
		steps[i].TaskID = taskID
	}
	r.steps[taskID] = steps
	return nil
}

func (r *memTasks) GetTaskSteps(ctx context.Context, taskID string) ([]model.TaskStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.TaskStep, len(r.steps[taskID]))
	copy(out, r.steps[taskID])
	return out, nil
}

func (r *memTasks) UpdateTaskStep(ctx context.Context, step *model.TaskStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	steps := r.steps[step.TaskID]
	for i := range steps {
		if steps[i].ID == step.ID {
			steps[i] = *step
			r.steps[step.TaskID] = steps
			return nil
		}
	}
	return fmt.Errorf("步骤不存在")
}

func (r *memTasks) GetTaskItems(ctx context.Context, taskID string) ([]model.TaskItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.TaskItem, len(r.items[taskID]))
	copy(out, r.items[taskID])
	return out, nil
}

func (r *memTasks) ReplaceTaskItems(ctx context.Context, taskID string, items []model.TaskItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]model.TaskItem, 0, len(items))
	for _, it := range r.items[taskID] {
		if it.Status == model.ItemStatusSuccess {
			kept = append(kept, it) // 已入数据集行保留
		}
	}
	for i := range items {
		r.seq++
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("item-%d", r.seq)
		}
		items[i].TaskID = taskID
		kept = append(kept, items[i])
	}
	r.items[taskID] = kept
	return nil
}

func (r *memTasks) UpdateItemResult(ctx context.Context, itemID, status, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for tid, items := range r.items {
		for i := range items {
			if items[i].ID == itemID {
				items[i].Status = status
				items[i].ErrorMessage = errMsg
				r.items[tid] = items
				return nil
			}
		}
	}
	return fmt.Errorf("明细不存在")
}

func (r *memTasks) RecoverStuck(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].Status == model.TaskStatusRunning {
			r.tasks[i].Status = model.TaskStatusPaused
			r.tasks[i].ErrorMessage = "服务重启，任务已暂停"
		}
	}
	for tid, steps := range r.steps {
		for i := range steps {
			if steps[i].Status == model.StepStatusRunning {
				steps[i].Status = model.StepStatusPaused
			}
		}
		r.steps[tid] = steps
	}
	return nil
}

/* ---- 假步骤执行器 ---- */

type fakeParse struct {
	text  string
	err   error
	block bool // 阻塞等待 ctx 取消（暂停测试）
}

func (f *fakeParse) Run(ctx context.Context, filename, filePath string, onProgress func(ParseProgress)) (string, error) {
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

/* ---- 数据集仓储假实现 + 假写入器 ---- */

type memDatasets struct {
	mu       sync.Mutex
	datasets []model.Dataset
	items    map[string][]model.DatasetItem
	seq      int
}

func newMemDatasets() *memDatasets {
	return &memDatasets{items: make(map[string][]model.DatasetItem)}
}

func (r *memDatasets) CreateDataset(ctx context.Context, d *model.Dataset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d.ID == "" { // 显式指定 ID（测试预置）时保留
		r.seq++
		d.ID = fmt.Sprintf("ds-%d", r.seq)
	}
	d.CreatedAt = time.Now()
	r.datasets = append(r.datasets, *d)
	return nil
}
func (r *memDatasets) UpdateDataset(ctx context.Context, d *model.Dataset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.datasets {
		if r.datasets[i].ID == d.ID {
			r.datasets[i] = *d
			return nil
		}
	}
	return fmt.Errorf("数据集不存在")
}
func (r *memDatasets) ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.Dataset
	for _, d := range r.datasets {
		if typ != "" && d.Type != typ {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
func (r *memDatasets) GetDataset(ctx context.Context, id string) (*model.Dataset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.datasets {
		if r.datasets[i].ID == id {
			d := r.datasets[i]
			return &d, nil
		}
	}
	return nil, fmt.Errorf("数据集不存在")
}
func (r *memDatasets) CountDatasets(ctx context.Context, typ string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.datasets)), nil
}
func (r *memDatasets) CountDatasetItems(ctx context.Context, typ string) (int64, error) {
	return 0, nil
}
func (r *memDatasets) ReplaceDatasetItems(ctx context.Context, id string, items []port.DatasetItemVector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.DatasetItem, len(items))
	for i, it := range items {
		out[i] = model.DatasetItem{ID: it.ID, DatasetID: id, Fields: it.Fields}
	}
	r.items[id] = out
	return nil
}
func (r *memDatasets) ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.DatasetItem
	for _, items := range r.items {
		out = append(out, items...)
	}
	return out, nil
}
func (r *memDatasets) ListDatasetItems(ctx context.Context, id string, limit int) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.items[id], nil
}
func (r *memDatasets) SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]port.SimilarDatasetItem, error) {
	return nil, nil
}

func (r *memDatasets) UpsertDatasetItems(ctx context.Context, datasetID, sourceTaskID string,
	items []port.DatasetItemVector, mode port.UpsertMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.items[datasetID]
	for _, it := range items {
		idx := -1
		for i := range list {
			if list[i].ItemKey != "" && list[i].ItemKey == it.ItemKey {
				idx = i
				break
			}
		}
		switch {
		case idx < 0:
			r.seq++
			list = append(list, model.DatasetItem{
				ID: fmt.Sprintf("di-%d", r.seq), DatasetID: datasetID,
				Fields: it.Fields, ItemKey: it.ItemKey,
				Fingerprint: it.Fingerprint, SourceTaskID: sourceTaskID,
			})
		case mode == port.UpsertUpdateExisting && list[idx].Fingerprint != it.Fingerprint:
			list[idx].Fields = it.Fields
			list[idx].Fingerprint = it.Fingerprint
			list[idx].SourceTaskID = sourceTaskID
		}
	}
	r.items[datasetID] = list
	return nil
}

func (r *memDatasets) DeleteDatasetItemsBySource(ctx context.Context, datasetID, sourceTaskID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept, removed []model.DatasetItem
	for _, it := range r.items[datasetID] {
		if it.SourceTaskID == sourceTaskID {
			removed = append(removed, it)
		} else {
			kept = append(kept, it)
		}
	}
	r.items[datasetID] = kept
	return int64(len(removed)), nil
}

func (r *memDatasets) GetDatasetItemKeyMap(ctx context.Context, datasetID string) (map[string]port.DatasetItemKeyInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]port.DatasetItemKeyInfo)
	for _, it := range r.items[datasetID] {
		if it.ItemKey != "" {
			out[it.ItemKey] = port.DatasetItemKeyInfo{ID: it.ID, Fingerprint: it.Fingerprint, SourceTaskID: it.SourceTaskID}
		}
	}
	return out, nil
}

func (r *memDatasets) CountDatasetItemsOfDataset(ctx context.Context, datasetID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.items[datasetID])), nil
}

func (r *memDatasets) ListDatasetItemsFiltered(ctx context.Context, f port.ItemFilter) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.DatasetItem
	for _, items := range r.items {
		for _, it := range items {
			if f.DatasetID != "" && it.DatasetID != f.DatasetID {
				continue
			}
			out = append(out, it)
		}
	}
	if len(out) > f.Limit && f.Limit > 0 {
		out = out[:f.Limit]
	}
	return out, nil
}

func (r *memDatasets) SearchSimilarDatasetItemsFiltered(ctx context.Context, vec []float32, f port.ItemFilter, n int) ([]port.SimilarDatasetItem, error) {
	return nil, nil
}

/* ---- 假写入器（生命周期测试：不关心分桶细节，全量 insert） ---- */

type fakeDatasetWriter struct {
	datasets port.DatasetRepo // 模拟落库（item_count 回填依赖真实条目数）
	written  int
	err      error
	block    bool // 阻塞等待 ctx 取消（暂停测试）
}

func (f *fakeDatasetWriter) Prepare(ctx context.Context, schema model.DatasetSchema, target DatasetTarget,
	taskID string, values []map[string]any) (*PreparedWrite, error) {
	target, err := target.Normalize()
	if err != nil {
		return nil, err
	}
	p := &PreparedWrite{Target: target, Schema: schema, Fresh: target.Mode == WriteModeCreate,
		preview: WritePreview{Mode: target.Mode, Total: len(values), Insert: len(values)}}
	for _, v := range values {
		p.Items = append(p.Items, PreparedItem{Values: v, Fields: marshalJSON(v), Action: ActionInsert})
	}
	return p, nil
}

func (f *fakeDatasetWriter) Write(ctx context.Context, datasetID, taskID string, prepared *PreparedWrite,
	report func(DatasetWriteProgress)) (WriteStats, error) {
	if f.block {
		<-ctx.Done()
		return WriteStats{}, ctx.Err()
	}
	if f.err != nil {
		return WriteStats{}, f.err
	}
	f.written = len(prepared.Items)
	if f.datasets != nil { // 模拟条目落库（count 回填走仓储）
		vecs := make([]port.DatasetItemVector, 0, len(prepared.Items))
		for _, it := range prepared.Items {
			vecs = append(vecs, port.DatasetItemVector{DatasetItem: model.DatasetItem{Fields: it.Fields}})
		}
		_ = f.datasets.ReplaceDatasetItems(ctx, datasetID, vecs)
	}
	return WriteStats{Written: f.written}, nil
}

// fakeEmbedder 不可用态的向量化桩（写入降级纯精确匹配路径）。
type fakeEmbedder struct{}

func (f *fakeEmbedder) Generate(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, nil
}
func (f *fakeEmbedder) Available() bool { return false }

/* ---- 阻塞型 scripted LLM（第二轮调用挂起，模拟分析进行中） ---- */

type blockingScriptedLLM struct {
	scripted scriptedLLM
	blocked  bool
	entered  chan struct{}
}

func (m *blockingScriptedLLM) Stream(ctx context.Context, cc *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
	if m.scripted.calls == 1 && !m.blocked {
		m.blocked = true
		close(m.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return m.scripted.Stream(ctx, cc, onEvent)
}

func (m *blockingScriptedLLM) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return m.Stream(ctx, cc, nil)
}
func (m *blockingScriptedLLM) Ping(ctx context.Context) error { return nil }

/* ---- 工具函数 ---- */

// waitTask 轮询等待任务进入期望状态（步骤 goroutine 异步执行）。
func waitTask(t *testing.T, repo *memTasks, id string, want func(*model.Task) bool) *model.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := repo.GetTask(context.Background(), id)
		if err == nil && want(task) {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待任务状态超时（当前: %+v）", mustTask(t, repo, id))
	return nil
}

func mustTask(t *testing.T, repo *memTasks, id string) *model.Task {
	t.Helper()
	task, err := repo.GetTask(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	return task
}

func mustSteps(t *testing.T, repo *memTasks, id string) []model.TaskStep {
	t.Helper()
	steps, err := repo.GetTaskSteps(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskSteps: %v", err)
	}
	return steps
}

func mustItems(t *testing.T, repo *memTasks, id string) []model.TaskItem {
	t.Helper()
	items, err := repo.GetTaskItems(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskItems: %v", err)
	}
	return items
}

func taskOfType(t *testing.T, repo *memTasks, typ string) *model.Task {
	t.Helper()
	lists, err := repo.ListTasks(context.Background(), port.TaskFilter{Type: typ})
	if err != nil || len(lists) == 0 {
		t.Fatalf("未找到 %s 任务: %v", typ, err)
	}
	return &lists[0]
}

func newTestManager(repo *memTasks, parse parseStepRunner, analyze analyzeStepRunner,
	datasets *memDatasets, writer datasetStepRunner) *TaskManager {
	if datasets == nil {
		datasets = newMemDatasets()
	}
	return NewTaskManager(repo, parse, analyze, datasets, writer)
}

/* ---- 用例 ---- */

func TestTaskCreateSeedsSteps(t *testing.T) {
	repo := newMemTasks()
	mgr := newTestManager(repo, &fakeParse{text: "解析结果"}, nil, nil, nil)

	ctx := context.Background()
	task, err := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Status != model.TaskStatusPending {
		t.Fatalf("新建任务状态 = %s", task.Status)
	}
	// 工作流定义快照：任务自描述（步骤链 + 依赖声明）
	if task.Workflow == "" || !strings.Contains(task.Workflow, `"kind":"parse"`) {
		t.Fatalf("新建任务应携带工作流快照: %s", task.Workflow)
	}
	steps := mustSteps(t, repo, task.ID)
	if len(steps) != 4 || steps[0].Name != "上传解析" || steps[3].Name != "生成数据集" {
		t.Fatalf("requirement_import 步骤 = %+v", steps)
	}

	if _, err := mgr.Create(ctx, "bug_import", "x"); err == nil {
		t.Fatal("第二波类型不应可创建")
	}
}

func TestTaskParseToGate(t *testing.T) {
	repo := newMemTasks()
	parse := &fakeParse{text: "## 解析后的需求全文"}
	mgr := newTestManager(repo, parse, nil, nil, nil)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	if err := mgr.TriggerParse(ctx, task.ID, "/tmp/upload.docx"); err != nil {
		t.Fatalf("TriggerParse: %v", err)
	}

	got := waitTask(t, repo, task.ID, func(t *model.Task) bool {
		return t.Status == model.TaskStatusAwaiting && t.CurrentStep == 2
	})
	if parse.text != inputParsedText(t, got) {
		t.Fatalf("解析文本未写入 input: %v", got.Input)
	}
	steps := mustSteps(t, repo, task.ID)
	if steps[0].Status != model.StepStatusSucceeded || steps[1].Status != model.StepStatusAwaiting {
		t.Fatalf("步骤状态 = %+v", steps)
	}
}

func TestTaskAnalyzePauseResume(t *testing.T) {
	repo := newMemTasks()
	parse := &fakeParse{text: testDoc}
	llm := &blockingScriptedLLM{scripted: scriptedLLM{responses: []*port.Message{
		toolCallMsg("c1", "read_document", `{}`),
		toolCallMsg("c2", "write_work_items", `{"items":[`+testDraftItem+`]}`),
		finalTextMsg("续跑完成"),
	}}, entered: make(chan struct{})}
	analyze := NewAnalyzeService(llm, "")
	analyze.EnableAgentMode(0)
	mgr := newTestManager(repo, parse, analyze, nil, nil)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	if err := mgr.TriggerAnalyze(ctx, task.ID); err != nil {
		t.Fatalf("TriggerAnalyze: %v", err)
	}
	<-llm.entered // 第二轮 loop 已挂起 → 暂停
	paused, err := mgr.Pause(ctx, task.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Status != model.TaskStatusPaused {
		t.Fatalf("暂停后状态 = %s", paused.Status)
	}
	// 检查点已落库：会话含 user/assistant(toolCall)/toolResult
	if paused.AgentContext == "" {
		t.Fatal("暂停应持久化会话检查点")
	}
	var cc port.Context
	if err := json.Unmarshal([]byte(paused.AgentContext), &cc); err != nil {
		t.Fatalf("检查点非法 JSON: %v", err)
	}
	if len(cc.Messages) != 3 {
		t.Fatalf("检查点消息 = %d 条（应为 user+assistant+toolResult）", len(cc.Messages))
	}
	if _, err := mgr.Pause(ctx, task.ID); err == nil {
		t.Fatal("重复暂停应报错")
	}

	// 继续：从检查点续跑 loop → 终稿 → 匹配导入门
	if _, err := mgr.Resume(ctx, task.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := waitTask(t, repo, task.ID, func(t *model.Task) bool {
		return t.Status == model.TaskStatusAwaiting && t.CurrentStep == 4
	})
	items := mustItems(t, repo, task.ID)
	if len(items) != 1 || items[0].Values()["title"] != "实现用户登录功能" {
		t.Fatalf("续跑产出明细 = %+v", items)
	}
	steps := mustSteps(t, repo, task.ID)
	if steps[2].Status != model.StepStatusSucceeded || steps[3].Status != model.StepStatusAwaiting {
		t.Fatalf("续跑后步骤 = %+v", steps)
	}
	// 最终会话含暂停前的轨迹 + 终稿（user/assistant/toolResult/assistant）
	var final port.Context
	if err := json.Unmarshal([]byte(got.AgentContext), &final); err != nil {
		t.Fatalf("最终会话非法 JSON: %v", err)
	}
	if len(final.Messages) != 6 {
		t.Fatalf("最终会话消息 = %d 条（user+读+写+终稿共 6 条）", len(final.Messages))
	}
}

func TestTaskAnalyzeFailureGoesToGate(t *testing.T) {
	repo := newMemTasks()
	parse := &fakeParse{text: testDoc}
	llm := &scriptedLLM{responses: []*port.Message{nil}} // agent 首轮即失败 → 降级也失败
	analyze := NewAnalyzeService(llm, "")
	analyze.EnableAgentMode(0)
	mgr := newTestManager(repo, parse, analyze, nil, nil)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	_ = mgr.TriggerAnalyze(ctx, task.ID)
	got := waitStepStatus(t, repo, task.ID, 3, model.StepStatusFailed) // AI 分析步骤失败
	if got.ErrorMessage == "" {
		t.Fatal("分析失败应记录错误信息")
	}
	if got.Status != model.TaskStatusAwaiting || got.CurrentStep != 2 {
		t.Fatalf("失败后应回到确认解析门: %s step=%d", got.Status, got.CurrentStep)
	}
	if got.AgentContext != "" {
		t.Fatal("失败重试应清空会话检查点")
	}
}

// waitStepStatus 轮询等待指定步骤进入期望状态（异步 goroutine 的确定性观察点）。
func waitStepStatus(t *testing.T, repo *memTasks, taskID string, seq int, want string) *model.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		steps := mustSteps(t, repo, taskID)
		for i := range steps {
			if steps[i].Seq == seq && steps[i].Status == want {
				return mustTask(t, repo, taskID)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待步骤 %d=%s 超时（当前: %+v）", seq, want, mustSteps(t, repo, taskID))
	return nil
}

func TestTaskGenerateDataset(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	writer := &fakeDatasetWriter{datasets: datasets}
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, datasets, writer)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	// 进入生成数据集门并预置草稿
	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{Fields: `{"title":"A"}`, Status: model.ItemStatusPending},
		{Fields: `{"title":"B"}`, Status: model.ItemStatusPending},
	})
	task.Status = model.TaskStatusAwaiting
	task.CurrentStep = 4
	_ = repo.UpdateTask(ctx, task)

	if err := mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeCreate, Name: "订单中心需求集"}); err != nil {
		t.Fatalf("TriggerGenerateDataset: %v", err)
	}
	got := waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusSucceeded })

	if got.OutputDatasetID == "" {
		t.Fatal("任务应回填产出数据集 ID")
	}
	ds, err := datasets.GetDataset(ctx, got.OutputDatasetID)
	if err != nil {
		t.Fatalf("数据集未创建: %v", err)
	}
	if ds.Name != "订单中心需求集" || ds.Status != model.DatasetStatusReady || ds.ItemCount != 2 {
		t.Fatalf("数据集状态 = %+v", ds)
	}
	if writer.written != 2 {
		t.Fatalf("写入条目 = %d", writer.written)
	}
	steps := mustSteps(t, repo, task.ID)
	if steps[3].Status != model.StepStatusSucceeded {
		t.Fatalf("生成数据集步骤 = %+v", steps[3])
	}
	// 草稿标记成功
	if items := mustItems(t, repo, task.ID); items[0].Status != model.ItemStatusSuccess {
		t.Fatalf("草稿未标记成功: %+v", items[0])
	}
}

func TestTaskGenerateDatasetFailureRetry(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	writer := &fakeDatasetWriter{datasets: datasets, err: fmt.Errorf("向量化失败")}
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, datasets, writer)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{Fields: `{"title":"A"}`, Status: model.ItemStatusPending},
	})
	task.Status = model.TaskStatusAwaiting
	task.CurrentStep = 4
	_ = repo.UpdateTask(ctx, task)

	_ = mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeCreate, Name: "订单中心需求集"})
	got := waitStepStatus(t, repo, task.ID, 4, model.StepStatusFailed) // 生成失败 → 门步骤 failed
	if got.Status != model.TaskStatusAwaiting || got.CurrentStep != 4 {
		t.Fatalf("失败后应回到生成数据集门: %s step=%d", got.Status, got.CurrentStep)
	}
	if got.OutputDatasetID == "" {
		t.Fatal("失败后应保留数据集引用（重试复用）")
	}

	// 重试成功：复用同一数据集
	writer.err = nil
	_ = mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeCreate, Name: "订单中心需求集"})
	got2 := waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusSucceeded })
	if got2.OutputDatasetID != got.OutputDatasetID {
		t.Fatal("重试应复用同一数据集")
	}
}

func TestTaskGenerateDatasetPauseResume(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	writer := &fakeDatasetWriter{datasets: datasets, block: true}
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, datasets, writer)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{Fields: `{"title":"A"}`, Status: model.ItemStatusPending},
	})
	task.Status = model.TaskStatusAwaiting
	task.CurrentStep = 4
	_ = repo.UpdateTask(ctx, task)

	_ = mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeCreate, Name: "订单中心需求集"})
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusRunning })
	paused, err := mgr.Pause(ctx, task.ID)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused.Status != model.TaskStatusPaused {
		t.Fatalf("暂停后状态 = %s", paused.Status)
	}

	// 继续：复用 building 数据集，写入完成 → 终态
	writer.block = false
	if _, err := mgr.Resume(ctx, task.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusSucceeded })
	ds, err := datasets.GetDataset(ctx, got.OutputDatasetID)
	if err != nil || ds.Status != model.DatasetStatusReady {
		t.Fatalf("续跑后数据集应 ready: %+v err=%v", ds, err)
	}
}

func TestTaskCompleteManual(t *testing.T) {
	repo := newMemTasks()
	parse := &fakeParse{text: "解析结果"}
	mgr := newTestManager(repo, parse, nil, nil, nil)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	done, err := mgr.Complete(ctx, task.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if done.Status != model.TaskStatusSucceeded {
		t.Fatalf("手动完成后状态 = %s", done.Status)
	}
	steps := mustSteps(t, repo, task.ID)
	if steps[1].Status != model.StepStatusSucceeded || steps[1].Detail != "人工完成" {
		t.Fatalf("门步骤 = %+v", steps[1])
	}
	if _, err := mgr.Complete(ctx, task.ID); err == nil {
		t.Fatal("终态不可重复完成")
	}
}

func TestTaskRecoverStuck(t *testing.T) {
	repo := newMemTasks()
	mgr := newTestManager(repo, &fakeParse{text: "x", block: true}, nil, nil, nil)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusRunning })

	// 模拟服务重启：running 任务 → paused
	if err := mgr.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	got := mustTask(t, repo, task.ID)
	if got.Status != model.TaskStatusPaused {
		t.Fatalf("重启后状态 = %s", got.Status)
	}
}

/* ---- 写入策略：分桶与幂等写入 ---- */

// seedTargetDataset 预置目标数据集：k1（task-9 写入，内容 A）、k2（他人写入，内容 B）。
func seedTargetDataset(t *testing.T, datasets *memDatasets) model.DatasetSchema {
	t.Helper()
	ctx := context.Background()
	_ = datasets.CreateDataset(ctx, &model.Dataset{ID: "ds-x", Type: model.DatasetTypeRequirement,
		Name: "已有需求集", Status: model.DatasetStatusReady})
	schema, ok := model.SchemaOf(model.DatasetTypeRequirement)
	if !ok {
		t.Fatal("requirement schema 未注册")
	}
	valuesA := map[string]any{"title": "需求A"}
	valuesB := map[string]any{"title": "需求B", "description": "原始描述"}
	_ = datasets.UpsertDatasetItems(ctx, "ds-x", "task-9", []port.DatasetItemVector{
		{DatasetItem: model.DatasetItem{Fields: marshalJSON(valuesA),
			ItemKey: logic.ItemKeyOf(schema, valuesA), Fingerprint: logic.FingerprintOf(schema, valuesA)}},
	}, port.UpsertInsertMissing)
	_ = datasets.UpsertDatasetItems(ctx, "ds-x", "task-other", []port.DatasetItemVector{
		{DatasetItem: model.DatasetItem{Fields: marshalJSON(valuesB),
			ItemKey: logic.ItemKeyOf(schema, valuesB), Fingerprint: logic.FingerprintOf(schema, valuesB)}},
	}, port.UpsertInsertMissing)
	return schema
}

func TestDatasetWriterUpsertBuckets(t *testing.T) {
	datasets := newMemDatasets()
	schema := seedTargetDataset(t, datasets)
	writer := NewDatasetWriter(&fakeEmbedder{}, datasets, 10)

	ctx := context.Background()
	// 待写：A（同 key 同内容）、B（同 key 新内容）、C（新 key）、空标题（非法）
	values := []map[string]any{
		{"title": "需求A"},
		{"title": "需求B", "description": "更新后的描述"},
		{"title": "需求C"},
		{"title": "", "priority": "High"},
	}

	prepared, err := writer.Prepare(ctx, schema, DatasetTarget{Mode: WriteModeUpsert, DatasetID: "ds-x"}, "task-10", values)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	pv := prepared.Preview()
	if pv.Insert != 1 || pv.Update != 1 || pv.Unchanged != 1 || pv.Invalid != 1 {
		t.Fatalf("upsert 分桶 = %+v", pv)
	}
	if _, err := writer.Write(ctx, "ds-x", "task-10", prepared, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if items := datasets.items["ds-x"]; len(items) != 3 {
		t.Fatalf("写入后条目 = %d（应为 3）", len(items))
	}
}

func TestDatasetWriterMergeSkipsConflicts(t *testing.T) {
	datasets := newMemDatasets()
	schema := seedTargetDataset(t, datasets)
	writer := NewDatasetWriter(&fakeEmbedder{}, datasets, 10)

	ctx := context.Background()
	values := []map[string]any{
		{"title": "需求A"}, // 已存在 → 跳过
		{"title": "需求B", "description": "更新的描述"}, // 已存在 → 跳过（merge 不更新）
		{"title": "需求C"}, // 新增
	}
	prepared, err := writer.Prepare(ctx, schema, DatasetTarget{Mode: WriteModeMerge, DatasetID: "ds-x"}, "task-10", values)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	pv := prepared.Preview()
	if pv.Insert != 1 || pv.Unchanged != 2 {
		t.Fatalf("merge 分桶 = %+v", pv)
	}
}

func TestDatasetWriterReplaceScope(t *testing.T) {
	datasets := newMemDatasets()
	schema := seedTargetDataset(t, datasets)
	writer := NewDatasetWriter(&fakeEmbedder{}, datasets, 10)

	ctx := context.Background()
	// task-9 重跑：k1（本任务旧条目）视同不存在 → insert；k2（他人条目）内容变化 → update
	values := []map[string]any{
		{"title": "需求A", "description": "重跑修订"},
		{"title": "需求B", "description": "也改了"},
	}
	prepared, err := writer.Prepare(ctx, schema, DatasetTarget{Mode: WriteModeReplace, DatasetID: "ds-x"}, "task-9", values)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	pv := prepared.Preview()
	if pv.Insert != 1 || pv.Update != 1 {
		t.Fatalf("replace 分桶 = %+v", pv)
	}
	if _, err := writer.Write(ctx, "ds-x", "task-9", prepared, nil); err != nil {
		t.Fatalf("Write: %v", err)
	}
	items := datasets.items["ds-x"]
	if len(items) != 2 {
		t.Fatalf("replace 后条目 = %d（应为 2）", len(items))
	}
}

func TestTaskRewriteDatasetAfterSucceeded(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	writer := &fakeDatasetWriter{datasets: datasets}
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, datasets, writer)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })

	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{Fields: `{"title":"A"}`, Status: model.ItemStatusPending},
	})
	task.Status = model.TaskStatusAwaiting
	task.CurrentStep = 4
	_ = repo.UpdateTask(ctx, task)

	_ = mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeCreate, Name: "需求集 v1"})
	got := waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusSucceeded })

	// 终态任务停留于数据集步骤：可换策略重写（幂等安全）
	target := DatasetTarget{Mode: WriteModeUpsert, DatasetID: got.OutputDatasetID}
	if err := mgr.TriggerGenerateDataset(ctx, task.ID, target); err != nil {
		t.Fatalf("终态重写应被允许: %v", err)
	}
	got2 := waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusSucceeded })
	if got2.OutputDatasetID != got.OutputDatasetID {
		t.Fatalf("upsert 目标应写入原数据集: %s", got2.OutputDatasetID)
	}
	if got2.FinishedAt.IsZero() {
		t.Fatal("重写完成后应回填完成时间")
	}
}

func TestTaskDatasetPreview(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	seedTargetDataset(t, datasets)
	writer := NewDatasetWriter(&fakeEmbedder{}, datasets, 10)
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, datasets, writer)

	ctx := context.Background()
	task, _ := mgr.Create(ctx, model.TaskTypeRequirementImport, "需求.docx")
	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{Fields: `{"title":"需求A"}`, Status: model.ItemStatusPending},
		{Fields: `{"title":"需求D"}`, Status: model.ItemStatusPending},
	})

	pv, err := mgr.PreviewDatasetWrite(ctx, task.ID, DatasetTarget{Mode: WriteModeMerge, DatasetID: "ds-x"})
	if err != nil {
		t.Fatalf("PreviewDatasetWrite: %v", err)
	}
	if pv.Insert != 1 || pv.Unchanged != 1 || pv.DatasetName != "已有需求集" {
		t.Fatalf("预览 = %+v", pv)
	}
}

/* ---- 测试辅助 ---- */

func inputParsedText(t *testing.T, task *model.Task) string {
	t.Helper()
	var in taskInputPayload
	if err := json.Unmarshal([]byte(task.Input), &in); err != nil {
		t.Fatalf("input 非法 JSON: %v", err)
	}
	return in.ParsedText
}
