package app

import (
	"context"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// Overview 仪表盘概览。
type Overview struct {
	Projects       int64           `json:"projects"`
	WorkItems      int64           `json:"workItems"`
	Datasets       int64           `json:"datasets"`
	DatasetItems   int64           `json:"datasetItems"`
	Tasks          int64           `json:"tasks"`
	RecentTasks    []model.Task    `json:"recentTasks"`
	RecentDatasets []model.Dataset `json:"recentDatasets"`
}

// OverviewService 概览用例：数据集/条目计数 + 最近任务。
type OverviewService struct {
	datasets port.DatasetRepo
	tasks    port.TaskRepo
}

// NewOverviewService 构造用例。
func NewOverviewService(datasets port.DatasetRepo, tasks port.TaskRepo) *OverviewService {
	return &OverviewService{datasets: datasets, tasks: tasks}
}

// Get 返回概览数据。
func (s *OverviewService) Get(ctx context.Context) (*Overview, error) {
	datasetCount, err := s.datasets.CountDatasets(ctx, "")
	if err != nil {
		return nil, err
	}
	itemCount, err := s.datasets.CountDatasetItems(ctx, "")
	if err != nil {
		return nil, err
	}
	recentDatasets, err := s.datasets.ListDatasets(ctx, "", 5)
	if err != nil {
		return nil, err
	}
	taskCount, err := s.tasks.CountTasks(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.tasks.ListTasks(ctx, port.TaskFilter{Limit: 5})
	if err != nil {
		return nil, err
	}
	return &Overview{
		Datasets: datasetCount, DatasetItems: itemCount, RecentDatasets: recentDatasets,
		Tasks: taskCount, RecentTasks: recent,
	}, nil
}
