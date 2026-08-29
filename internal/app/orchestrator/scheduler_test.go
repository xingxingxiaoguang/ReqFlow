package orchestrator

import (
	"context"
	"testing"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestSchedulerUsesStepIdentityForRepeatedKindsAndFinalOutputs(t *testing.T) {
	definition := model.TaskDefinition{
		Key: "double_extract", Name: "双阶段抽取",
		InputPorts: map[string]model.PortDefinition{
			"source": {ResourceType: model.ResourceAssetSet, Required: true},
		},
		OutputPorts: map[string]model.PortDefinition{
			"batch": {ResourceType: model.ResourceDatasetBatch},
		},
		OutputBindings: map[string]string{"batch": "$step.refine.batch"},
		Steps: []model.StepDefinition{
			{ID: "extract", Name: "抽取", Kind: model.StepKindLLMExtract,
				Inputs:  map[string]string{"source": "$task.source"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts}},
			{ID: "refine", Name: "修订", Kind: model.StepKindLLMExtract, DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}},
		},
	}
	snapshot, _, err := logic.NormalizeTaskDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	repo := &schedulerMemoryRepo{execution: model.TaskExecution{
		Task: model.Task{ID: "task-1", Status: model.TaskStatusRunning, DefinitionSnapshot: string(snapshot)},
		Inputs: []model.TaskResourceBinding{{PortName: "source", Direction: model.ResourceInput,
			ResourceType: model.ResourceAssetSet, ResourceID: "asset-set-1"}},
		Steps: []model.StepRun{
			{ID: "run-extract", TaskID: "task-1", StepID: "extract", Kind: model.StepKindLLMExtract, Status: model.StepRunPending},
			{ID: "run-refine", TaskID: "task-1", StepID: "refine", Kind: model.StepKindLLMExtract, Status: model.StepRunPending},
		},
	}}
	scheduler := NewScheduler(repo)
	ctx := context.Background()
	if err := scheduler.Schedule(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if got := repo.execution.Steps[0].Status; got != model.StepRunQueued {
		t.Fatalf("首步骤状态=%s", got)
	}
	if got := repo.execution.Steps[1].Status; got != model.StepRunPending {
		t.Fatalf("依赖未完成时 refine 不应排队: %s", got)
	}

	repo.execution.Steps[0].Status = model.StepRunSucceeded
	repo.execution.StepOutputs = append(repo.execution.StepOutputs, model.StepResourceBinding{
		StepRunID: "run-extract", PortName: "drafts", ResourceType: model.ResourceRecordDrafts, ResourceID: "drafts-1",
	})
	if err := scheduler.Schedule(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if got := repo.execution.Steps[1].Status; got != model.StepRunQueued {
		t.Fatalf("refine 应按 step_id 独立排队: %s", got)
	}

	repo.execution.Steps[1].Status = model.StepRunSucceeded
	repo.execution.StepOutputs = append(repo.execution.StepOutputs, model.StepResourceBinding{
		StepRunID: "run-refine", PortName: "batch", ResourceType: model.ResourceDatasetBatch, ResourceID: "batch-1",
	})
	if err := scheduler.Schedule(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if repo.execution.Task.Status != model.TaskStatusSucceeded || len(repo.taskOutputs) != 1 {
		t.Fatalf("任务未随最终输出原子收束: status=%s outputs=%+v", repo.execution.Task.Status, repo.taskOutputs)
	}
	if repo.taskOutputs[0].PortName != "batch" || repo.taskOutputs[0].ResourceID != "batch-1" {
		t.Fatalf("任务输出解析错误: %+v", repo.taskOutputs)
	}
}

func TestSchedulerMovesArbitraryHumanGateToAwaiting(t *testing.T) {
	definition := model.TaskDefinition{
		Key: "review_first", Name: "先审核",
		OutputPorts:    map[string]model.PortDefinition{"approved": {ResourceType: model.ResourceApprovedRecords}},
		OutputBindings: map[string]string{"approved": "$step.review.approved"},
		Steps: []model.StepDefinition{{ID: "review", Name: "审核", Kind: model.StepKindHumanReview,
			Outputs: map[string]model.ResourceType{"approved": model.ResourceApprovedRecords}}},
	}
	snapshot, _, err := logic.NormalizeTaskDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	repo := &schedulerMemoryRepo{execution: model.TaskExecution{
		Task: model.Task{ID: "task-review", Status: model.TaskStatusRunning, DefinitionSnapshot: string(snapshot)},
		Steps: []model.StepRun{{ID: "run-review", TaskID: "task-review", StepID: "review",
			Kind: model.StepKindHumanReview, Status: model.StepRunPending}},
	}}
	if err := NewScheduler(repo).Schedule(context.Background(), "task-review"); err != nil {
		t.Fatal(err)
	}
	if repo.execution.Steps[0].Status != model.StepRunAwaiting || repo.execution.Task.Status != model.TaskStatusAwaiting {
		t.Fatalf("人工 Gate 状态未收敛: %+v", repo.execution)
	}
}

type schedulerMemoryRepo struct {
	execution   model.TaskExecution
	taskOutputs []model.TaskResourceBinding
}

func (r *schedulerMemoryRepo) GetTaskExecution(context.Context, string) (*model.TaskExecution, error) {
	clone := r.execution
	clone.Inputs = append([]model.TaskResourceBinding(nil), r.execution.Inputs...)
	clone.Steps = append([]model.StepRun(nil), r.execution.Steps...)
	clone.StepOutputs = append([]model.StepResourceBinding(nil), r.execution.StepOutputs...)
	return &clone, nil
}
func (r *schedulerMemoryRepo) GetTaskResourceBindings(context.Context, string) ([]model.TaskResourceBinding, error) {
	return append([]model.TaskResourceBinding(nil), r.execution.Inputs...), nil
}
func (r *schedulerMemoryRepo) GetStepRuns(context.Context, string) ([]model.StepRun, error) {
	return append([]model.StepRun(nil), r.execution.Steps...), nil
}
func (r *schedulerMemoryRepo) GetStepResourceBindings(context.Context, string) ([]model.StepResourceBinding, error) {
	return append([]model.StepResourceBinding(nil), r.execution.StepOutputs...), nil
}
func (r *schedulerMemoryRepo) ListSchedulableTaskIDs(context.Context, int) ([]string, error) {
	return []string{r.execution.Task.ID}, nil
}
func (r *schedulerMemoryRepo) QueueReadySteps(_ context.Context, _ string, queued, awaiting []port.StepQueueEntry) error {
	queuedSet, awaitingSet := map[string]bool{}, map[string]bool{}
	inputHashes := map[string]string{}
	for _, entry := range queued {
		queuedSet[entry.StepRunID] = true
		inputHashes[entry.StepRunID] = entry.InputHash
	}
	for _, entry := range awaiting {
		awaitingSet[entry.StepRunID] = true
		inputHashes[entry.StepRunID] = entry.InputHash
	}
	active := false
	hasAwaiting := false
	for i := range r.execution.Steps {
		run := &r.execution.Steps[i]
		if run.Status == model.StepRunPending && queuedSet[run.ID] {
			run.Status = model.StepRunQueued
			run.InputHash = inputHashes[run.ID]
		}
		if run.Status == model.StepRunPending && awaitingSet[run.ID] {
			run.Status = model.StepRunAwaiting
			run.InputHash = inputHashes[run.ID]
		}
		active = active || run.Status == model.StepRunQueued || run.Status == model.StepRunRunning
		hasAwaiting = hasAwaiting || run.Status == model.StepRunAwaiting
	}
	if !active && hasAwaiting {
		r.execution.Task.Status = model.TaskStatusAwaiting
	}
	return nil
}
func (r *schedulerMemoryRepo) CompleteTask(_ context.Context, _ string, outputs []model.TaskResourceBinding) error {
	r.taskOutputs = append([]model.TaskResourceBinding(nil), outputs...)
	r.execution.Task.Status = model.TaskStatusSucceeded
	return nil
}
