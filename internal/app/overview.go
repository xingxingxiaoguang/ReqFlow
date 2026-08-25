package app

import (
	"context"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// Overview 仪表盘概览。
type Overview struct {
	Projects      int64                `json:"projects"`
	WorkItems     int64                `json:"workItems"`
	Records       int64                `json:"records"`
	RecentRecords []model.ImportRecord `json:"recentRecords"`
}

// OverviewService 概览用例：本地缓存计数 + 最近导入记录。
type OverviewService struct {
	projects  port.ProjectRepo
	workItems port.WorkItemRepo
	records   port.ImportRepo
}

// NewOverviewService 构造用例。
func NewOverviewService(projects port.ProjectRepo, workItems port.WorkItemRepo, records port.ImportRepo) *OverviewService {
	return &OverviewService{projects: projects, workItems: workItems, records: records}
}

// Get 返回概览数据。
func (s *OverviewService) Get(ctx context.Context) (*Overview, error) {
	projects, err := s.projects.CountActive(ctx)
	if err != nil {
		return nil, err
	}
	workItems, err := s.workItems.CountActive(ctx)
	if err != nil {
		return nil, err
	}
	recent, err := s.records.ListRecords(ctx, 5)
	if err != nil {
		return nil, err
	}
	return &Overview{
		Projects: projects, WorkItems: workItems,
		Records: int64(len(recent)), RecentRecords: recent,
	}, nil
}
