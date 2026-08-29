package orchestrator

import (
	"context"
	"fmt"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type TaskCanceler interface {
	CancelTask(taskID string) bool
}

// RuntimeService 是 V2 Task 生命周期的唯一写入口。状态先持久化，再通知本地
// Worker；其他进程中的 Worker 会通过续租看到 pausing 并自行收束。
type RuntimeService struct {
	repo      port.OrchestratorLifecycleRepo
	scheduler *Scheduler
	canceler  TaskCanceler
}

func NewRuntimeService(repo port.OrchestratorLifecycleRepo, scheduler *Scheduler, canceler TaskCanceler) (*RuntimeService, error) {
	if repo == nil {
		return nil, fmt.Errorf("orchestrator: lifecycle repo is required")
	}
	if scheduler == nil {
		return nil, fmt.Errorf("orchestrator: scheduler is required")
	}
	return &RuntimeService{repo: repo, scheduler: scheduler, canceler: canceler}, nil
}

func (s *RuntimeService) Start(ctx context.Context, taskID string) error {
	if err := s.repo.StartTask(ctx, taskID); err != nil {
		return err
	}
	return s.scheduler.Schedule(ctx, taskID)
}

func (s *RuntimeService) Pause(ctx context.Context, taskID string) error {
	if err := s.repo.RequestTaskPause(ctx, taskID); err != nil {
		return err
	}
	if s.canceler != nil {
		s.canceler.CancelTask(taskID)
	}
	return nil
}

func (s *RuntimeService) Resume(ctx context.Context, taskID string) error {
	if err := s.repo.ResumeTask(ctx, taskID); err != nil {
		return err
	}
	return s.scheduler.Schedule(ctx, taskID)
}

func (s *RuntimeService) Retry(ctx context.Context, taskID, stepID string) error {
	if err := s.repo.RetryStep(ctx, taskID, stepID); err != nil {
		return err
	}
	return s.scheduler.Schedule(ctx, taskID)
}

func (s *RuntimeService) ApproveHumanStep(ctx context.Context, taskID, stepID string, result StepResult) error {
	execution, err := s.repo.GetTaskExecution(ctx, taskID)
	if err != nil {
		return err
	}
	definition, err := executionDefinition(execution)
	if err != nil {
		return err
	}
	var stepDef model.StepDefinition
	var run model.StepRun
	for _, candidate := range definition.Steps {
		if candidate.ID == stepID {
			stepDef = candidate
			break
		}
	}
	for _, candidate := range execution.Steps {
		if candidate.StepID == stepID {
			run = candidate
			break
		}
	}
	if stepDef.ID == "" || run.ID == "" {
		return fmt.Errorf("任务 %s 不存在步骤 %s", taskID, stepID)
	}
	if stepDef.Kind != model.StepKindHumanReview || run.Status != model.StepRunAwaiting {
		return fmt.Errorf("%w: 步骤 %s 不是等待中的人工 Gate", port.ErrInvalidTransition, stepID)
	}
	outputs, err := validateOutputs(stepDef, result)
	if err != nil {
		return err
	}
	for i := range outputs {
		outputs[i].StepRunID = run.ID
	}
	if err := s.repo.CompleteAwaitingStep(ctx, run.ID, outputs); err != nil {
		return err
	}
	return s.scheduler.Schedule(ctx, taskID)
}

func (s *RuntimeService) Get(ctx context.Context, taskID string) (*model.TaskExecution, error) {
	return s.repo.GetTaskExecution(ctx, taskID)
}
