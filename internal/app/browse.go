package app

import (
	"context"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// BrowseFilter 已同步数据浏览查询。
type BrowseFilter struct {
	ProjectID string `form:"project_id"`
	Search    string `form:"search"`
	Limit     int    `form:"limit"`
	Offset    int    `form:"offset"`
}

// BrowseService 已同步项目/工作项的浏览用例（前端「数据同步」页列表）。
type BrowseService struct {
	projects  port.ProjectRepo
	workItems port.WorkItemRepo
}

// NewBrowseService 构造用例。
func NewBrowseService(projects port.ProjectRepo, workItems port.WorkItemRepo) *BrowseService {
	return &BrowseService{projects: projects, workItems: workItems}
}

// ListProjects 已同步的活跃项目。
func (s *BrowseService) ListProjects(ctx context.Context) ([]model.Project, error) {
	return s.projects.ListActive(ctx)
}

// ListWorkItems 已同步的活跃工作项（分页 + 项目过滤 + 标题/编号搜索）。
func (s *BrowseService) ListWorkItems(ctx context.Context, f BrowseFilter) ([]model.WorkItem, int64, error) {
	return s.workItems.ListActive(ctx, port.WorkItemFilter{
		ProjectID: f.ProjectID, Search: f.Search, Limit: f.Limit, Offset: f.Offset,
	})
}
