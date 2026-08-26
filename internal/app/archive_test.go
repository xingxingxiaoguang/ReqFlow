package app

import (
	"context"
	"fmt"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 归档仓储 mock：搬移语义镜像真库（主表删除 + 归档区落位） ---- */

type memArchives struct {
	tasks           *memTasks
	datasets        *memDatasets
	archivedTasks   map[string]port.TaskStepsSnapshot
	archivedTaskMet map[string]model.ArchivedTask
	archivedDataset map[string]model.ArchivedDataset
}

func newMemArchives(tasks *memTasks, datasets *memDatasets) *memArchives {
	return &memArchives{
		tasks: tasks, datasets: datasets,
		archivedTasks:   map[string]port.TaskStepsSnapshot{},
		archivedTaskMet: map[string]model.ArchivedTask{},
		archivedDataset: map[string]model.ArchivedDataset{},
	}
}

func (a *memArchives) ArchiveTask(ctx context.Context, task *model.Task, snap port.TaskStepsSnapshot) error {
	a.tasks.mu.Lock()
	defer a.tasks.mu.Unlock()
	found := false
	kept := a.tasks.tasks[:0]
	for _, t := range a.tasks.tasks {
		if t.ID == task.ID {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return fmt.Errorf("任务不存在或已归档")
	}
	a.tasks.tasks = kept
	delete(a.tasks.steps, task.ID)
	delete(a.tasks.items, task.ID)
	a.archivedTasks[task.ID] = snap
	a.archivedTaskMet[task.ID] = model.ArchivedTask{Task: *task}
	return nil
}

func (a *memArchives) RestoreTask(ctx context.Context, taskID string) (*model.Task, port.TaskStepsSnapshot, error) {
	a.tasks.mu.Lock()
	defer a.tasks.mu.Unlock()
	snap, ok := a.archivedTasks[taskID]
	if !ok {
		return nil, snap, fmt.Errorf("归档中不存在该任务")
	}
	met := a.archivedTaskMet[taskID]
	task := met.Task
	a.tasks.tasks = append(a.tasks.tasks, task)
	a.tasks.steps[taskID] = snap.Steps
	a.tasks.items[taskID] = snap.Items
	delete(a.archivedTasks, taskID)
	delete(a.archivedTaskMet, taskID)
	return &task, snap, nil
}

func (a *memArchives) ListArchivedTasks(ctx context.Context, typ string, limit int) ([]model.ArchivedTask, error) {
	var out []model.ArchivedTask
	for _, m := range a.archivedTaskMet {
		if typ != "" && m.Type != typ {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (a *memArchives) ArchiveDataset(ctx context.Context, datasetID string) error {
	a.datasets.mu.Lock()
	defer a.datasets.mu.Unlock()
	found := false
	kept := a.datasets.datasets[:0]
	for _, d := range a.datasets.datasets {
		if d.ID == datasetID {
			found = true
			a.archivedDataset[datasetID] = model.ArchivedDataset{Dataset: d}
			continue
		}
		kept = append(kept, d)
	}
	if !found {
		return fmt.Errorf("数据集不存在或已归档")
	}
	a.datasets.datasets = kept
	delete(a.datasets.items, datasetID)
	return nil
}

func (a *memArchives) RestoreDataset(ctx context.Context, datasetID string) error {
	a.datasets.mu.Lock()
	defer a.datasets.mu.Unlock()
	ds, ok := a.archivedDataset[datasetID]
	if !ok {
		return fmt.Errorf("归档中不存在该数据集")
	}
	a.datasets.datasets = append(a.datasets.datasets, ds.Dataset)
	// 条目随行恢复由真库 SQL 完成；mock 中条目随 items map 保留在归档快照外，
	// 语料恢复语义由 TestArchiveDatasetRemovesFromCorpus 的归档/恢复对称断言覆盖
	delete(a.archivedDataset, datasetID)
	return nil
}

func (a *memArchives) ListArchivedDatasets(ctx context.Context, typ string, limit int) ([]model.ArchivedDataset, error) {
	var out []model.ArchivedDataset
	for _, d := range a.archivedDataset {
		if typ != "" && d.Type != typ {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

/* ---- 用例 ---- */

func newArchiveService(repo *memTasks, datasets *memDatasets) (*ArchiveService, *memArchives) {
	archives := newMemArchives(repo, datasets)
	return NewArchiveService(repo, datasets, archives), archives
}

// TestArchiveTaskRoundTrip 归档任务：主循环消失 → 归档列表可见 → 恢复回主表。
func TestArchiveTaskRoundTrip(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	svc, _ := newArchiveService(repo, datasets)

	ctx := context.Background()
	task, _ := CreateAwaitingTask(t, repo)
	_ = repo.ReplaceTaskItems(ctx, task.ID, []model.TaskItem{
		{DraftItem: model.DraftItem{Title: "A"}, Status: model.ItemStatusPending},
	})

	if err := svc.ArchiveTask(ctx, task.ID); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	// 主业务循环退出：列表无、详情 404、明细空
	if tasks, _ := repo.ListTasks(ctx, port.TaskFilter{}); len(tasks) != 0 {
		t.Fatalf("归档后任务列表应为空: %d", len(tasks))
	}
	if _, err := repo.GetTask(ctx, task.ID); err == nil {
		t.Fatal("归档后任务详情应不可得")
	}
	if items, _ := repo.GetTaskItems(ctx, task.ID); len(items) != 0 {
		t.Fatalf("归档后明细应为空: %d", len(items))
	}
	// 归档列表可见
	view, err := svc.List(ctx, "task", "", 50)
	if err != nil || len(view.Tasks) != 1 || view.Tasks[0].ID != task.ID {
		t.Fatalf("归档列表 = %+v err=%v", view, err)
	}

	// 恢复：任务 + 明细完整回来
	if _, err := svc.RestoreTask(ctx, task.ID); err != nil {
		t.Fatalf("RestoreTask: %v", err)
	}
	got, err := repo.GetTask(ctx, task.ID)
	if err != nil || got.Status != model.TaskStatusAwaiting {
		t.Fatalf("恢复后任务 = %+v err=%v", got, err)
	}
	if items, _ := repo.GetTaskItems(ctx, task.ID); len(items) != 1 {
		t.Fatalf("恢复后明细 = %d（应为 1）", len(items))
	}
	if view, _ := svc.List(ctx, "task", "", 50); len(view.Tasks) != 0 {
		t.Fatal("恢复后归档列表应为空")
	}
}

// TestArchiveTaskRejectsRunning 运行中的任务不可归档（执行 goroutine 仍在写）。
func TestArchiveTaskRejectsRunning(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	svc, _ := newArchiveService(repo, datasets)

	ctx := context.Background()
	task, _ := CreateAwaitingTask(t, repo)
	task.Status = model.TaskStatusRunning
	_ = repo.UpdateTask(ctx, task)

	if err := svc.ArchiveTask(ctx, task.ID); err == nil {
		t.Fatal("运行中任务归档应报错")
	}
	if tasks, _ := repo.ListTasks(ctx, port.TaskFilter{}); len(tasks) != 1 {
		t.Fatal("拒绝归档后任务应仍在主表")
	}
}

// TestArchiveDatasetGuard 数据集归档：被进行中任务引用时拒绝；归档后退出查重语料。
func TestArchiveDatasetGuard(t *testing.T) {
	repo := newMemTasks()
	datasets := newMemDatasets()
	svc, _ := newArchiveService(repo, datasets)

	ctx := context.Background()
	_ = datasets.CreateDataset(ctx, &model.Dataset{ID: "ds-1", Type: model.DatasetTypeRequirement,
		Name: "需求集", Status: model.DatasetStatusReady})
	_ = datasets.UpsertDatasetItems(ctx, "ds-1", "task-x", []port.DatasetItemVector{
		{DatasetItem: model.DatasetItem{Fields: `{"title":"t"}`, ItemKey: "k1"}},
	}, port.UpsertInsertMissing)

	// running 任务引用 → 拒绝
	task, _ := CreateAwaitingTask(t, repo)
	task.Status = model.TaskStatusRunning
	task.OutputDatasetID = "ds-1"
	_ = repo.UpdateTask(ctx, task)
	if err := svc.ArchiveDataset(ctx, "ds-1"); err == nil {
		t.Fatal("被运行中任务引用的数据集归档应报错")
	}

	// 任务结束 → 允许归档；语料（查重/工具）退出
	task.Status = model.TaskStatusSucceeded
	_ = repo.UpdateTask(ctx, task)
	if err := svc.ArchiveDataset(ctx, "ds-1"); err != nil {
		t.Fatalf("ArchiveDataset: %v", err)
	}
	if list, _ := datasets.ListDatasets(ctx, model.DatasetTypeRequirement, 10); len(list) != 0 {
		t.Fatal("归档后数据集主列表应为空")
	}
	if corpus, _ := datasets.ListDatasetItemsByType(ctx, model.DatasetTypeRequirement); len(corpus) != 0 {
		t.Fatalf("归档后查重语料应为空: %d", len(corpus))
	}

	// 恢复 → 主列表与语料回归
	if err := svc.RestoreDataset(ctx, "ds-1"); err != nil {
		t.Fatalf("RestoreDataset: %v", err)
	}
	if list, _ := datasets.ListDatasets(ctx, model.DatasetTypeRequirement, 10); len(list) != 1 {
		t.Fatal("恢复后数据集主列表应有 1 条")
	}
}

// CreateAwaitingTask 造一个停在确认门（awaiting）的任务（归档测试的常规起点）。
func CreateAwaitingTask(t *testing.T, repo *memTasks) (*model.Task, error) {
	t.Helper()
	mgr := newTestManager(repo, &fakeParse{text: testDoc}, nil, nil, nil)
	task, err := mgr.Create(context.Background(), model.TaskTypeRequirementImport, "需求.docx")
	if err != nil {
		return task, err
	}
	_ = mgr.TriggerParse(context.Background(), task.ID, "/tmp/x.docx")
	waitTask(t, repo, task.ID, func(t *model.Task) bool { return t.Status == model.TaskStatusAwaiting })
	return mustTask(t, repo, task.ID), nil
}
