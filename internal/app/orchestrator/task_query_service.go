package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// TaskQueryService 是 V2 Task 的独立读用例。任务生命周期仍只允许 RuntimeService
// 修改，目录查询不会扩大写服务的职责。
type TaskQueryService struct {
	repo port.OrchestratorTaskQueryRepo
}

func NewTaskQueryService(repo port.OrchestratorTaskQueryRepo) (*TaskQueryService, error) {
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: task query repo is required")
	}
	return &TaskQueryService{repo: repo}, nil
}

type TaskQuery struct {
	WorkspaceID string
	Status      string
	Limit       int
}

func (s *TaskQueryService) List(ctx context.Context, query TaskQuery) ([]model.Task, error) {
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	query.Status = strings.TrimSpace(query.Status)
	if query.WorkspaceID == "" {
		query.WorkspaceID = "default"
	}
	if query.Status != "" && !validOrchestratorTaskStatus(query.Status) {
		return nil, fmt.Errorf("V2 Task status 非法: %s", query.Status)
	}
	if query.Limit <= 0 || query.Limit > 200 {
		query.Limit = 50
	}
	return s.repo.ListOrchestratorTasks(ctx, port.OrchestratorTaskFilter{
		WorkspaceID: query.WorkspaceID, Status: query.Status, Limit: query.Limit,
	})
}

func validOrchestratorTaskStatus(status string) bool {
	switch status {
	case model.TaskStatusPending, model.TaskStatusRunning, model.TaskStatusPausing,
		model.TaskStatusAwaiting, model.TaskStatusPaused, model.TaskStatusSucceeded,
		model.TaskStatusFailed:
		return true
	default:
		return false
	}
}
