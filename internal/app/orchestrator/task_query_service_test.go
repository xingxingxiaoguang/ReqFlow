package orchestrator

import (
	"context"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestTaskQueryServiceNormalizesAndReturnsV2Tasks(t *testing.T) {
	repo := &taskQueryRepoStub{tasks: []model.Task{{ID: "task-1", DefinitionID: "definition-1"}}}
	service, err := NewTaskQueryService(repo)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := service.List(context.Background(), TaskQuery{Status: model.TaskStatusAwaiting, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || repo.filter.WorkspaceID != "default" || repo.filter.Status != model.TaskStatusAwaiting || repo.filter.Limit != 50 {
		t.Fatalf("查询未按 V2 规则归一化: tasks=%+v filter=%+v", tasks, repo.filter)
	}
}

func TestTaskQueryServiceRejectsUnknownStatus(t *testing.T) {
	service, err := NewTaskQueryService(&taskQueryRepoStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(context.Background(), TaskQuery{Status: "archived"}); err == nil {
		t.Fatal("未知 V2 Task 状态应被拒绝")
	}
}

type taskQueryRepoStub struct {
	filter port.OrchestratorTaskFilter
	tasks  []model.Task
}

func (r *taskQueryRepoStub) ListOrchestratorTasks(_ context.Context, filter port.OrchestratorTaskFilter) ([]model.Task, error) {
	r.filter = filter
	return append([]model.Task(nil), r.tasks...), nil
}
