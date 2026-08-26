package app

import (
	"context"
	"fmt"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ArchiveService 归档用例：任务/数据集的删除入档与恢复。
// 已归档数据物理上离开主表——列表、查重语料、语义检索、统计等主业务循环自动不再触达；
// 归档列表独立查询，支持原样恢复（数据集条目向量原生保留）。
type ArchiveService struct {
	tasks    port.TaskRepo
	datasets port.DatasetRepo
	archives port.ArchiveRepo
}

// 归档种类（HTTP 层用；转发 domain 常量，httpgin 不直接依赖 domain）。
const (
	ArchiveKindTask    = model.ArchiveKindTask
	ArchiveKindDataset = model.ArchiveKindDataset
)

// NewArchiveService 构造用例。
func NewArchiveService(tasks port.TaskRepo, datasets port.DatasetRepo, archives port.ArchiveRepo) *ArchiveService {
	return &ArchiveService{tasks: tasks, datasets: datasets, archives: archives}
}

// ArchiveTask 归档任务（含步骤与明细快照）。运行中的任务不可归档（执行 goroutine 仍在写）。
func (s *ArchiveService) ArchiveTask(ctx context.Context, taskID string) error {
	task, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("任务不存在: %w", err)
	}
	if task.Status == model.TaskStatusRunning {
		return fmt.Errorf("任务正在运行，请先暂停或等待完成后再归档")
	}
	steps, err := s.tasks.GetTaskSteps(ctx, taskID)
	if err != nil {
		return err
	}
	items, err := s.tasks.GetTaskItems(ctx, taskID)
	if err != nil {
		return err
	}
	return s.archives.ArchiveTask(ctx, task, port.TaskStepsSnapshot{Steps: steps, Items: items})
}

// ArchiveDataset 归档数据集（含条目与向量）。有进行中任务引用（产出/消费）时拒绝。
// 注意：产出该数据集的任务不受影响——任务与数据集独立归档。
func (s *ArchiveService) ArchiveDataset(ctx context.Context, datasetID string) error {
	if _, err := s.datasets.GetDataset(ctx, datasetID); err != nil {
		return fmt.Errorf("数据集不存在: %w", err)
	}
	running, err := s.tasks.ListTasks(ctx, port.TaskFilter{Status: model.TaskStatusRunning, Limit: 200})
	if err != nil {
		return err
	}
	for i := range running {
		if running[i].OutputDatasetID == datasetID || running[i].InputDatasetID == datasetID {
			return fmt.Errorf("任务「%s」正在引用该数据集，请等待其完成或暂停后归档", running[i].Title)
		}
	}
	return s.archives.ArchiveDataset(ctx, datasetID)
}

// RestoreTask 归档任务恢复到主表（可继续未走完的流程）。
func (s *ArchiveService) RestoreTask(ctx context.Context, taskID string) (*model.Task, error) {
	task, _, err := s.archives.RestoreTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// RestoreDataset 归档数据集（含条目）恢复到主表（查重/检索语料随之生效）。
func (s *ArchiveService) RestoreDataset(ctx context.Context, datasetID string) error {
	return s.archives.RestoreDataset(ctx, datasetID)
}

// ArchiveView 归档列表视图（kind 分开的两个集合，按需取用）。
type ArchiveView struct {
	Tasks    []model.ArchivedTask    `json:"tasks"`
	Datasets []model.ArchivedDataset `json:"datasets"`
}

// List 归档列表（kind = task | dataset；typ 按任务/数据集类型过滤）。
func (s *ArchiveService) List(ctx context.Context, kind, typ string, limit int) (*ArchiveView, error) {
	view := &ArchiveView{}
	switch kind {
	case "", model.ArchiveKindTask:
		tasks, err := s.archives.ListArchivedTasks(ctx, typ, limit)
		if err != nil {
			return nil, err
		}
		view.Tasks = tasks
	}
	if kind == "" || kind == model.ArchiveKindDataset {
		datasets, err := s.archives.ListArchivedDatasets(ctx, typ, limit)
		if err != nil {
			return nil, err
		}
		view.Datasets = datasets
	}
	return view, nil
}
