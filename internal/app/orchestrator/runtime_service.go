package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// HumanStepContext 是领域审核用例读取人工 Gate 的受信上下文。外部请求不再提供
// ResourceRef；具体输入只从 TaskDefinition 快照和已持久化的上游输出解析。
type HumanStepContext struct {
	Definition model.StepDefinition
	Run        model.StepRun
	Inputs     map[string]model.ResourceRef
}

func (s *RuntimeService) GetHumanStepContext(ctx context.Context, taskID, stepID string) (*HumanStepContext, error) {
	execution, err := s.repo.GetTaskExecution(ctx, taskID)
	if err != nil {
		return nil, err
	}
	definition, err := executionDefinition(execution)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("任务 %s 不存在步骤 %s", taskID, stepID)
	}
	if stepDef.Kind != model.StepKindHumanReview {
		return nil, fmt.Errorf("%w: 步骤 %s 不是人工 Gate", port.ErrInvalidTransition, stepID)
	}
	inputs, err := resolveStepInputs(stepDef, execution)
	if err != nil {
		return nil, err
	}
	return &HumanStepContext{Definition: stepDef, Run: run, Inputs: inputs}, nil
}

func (s *RuntimeService) ApproveHumanStep(ctx context.Context, taskID, stepID string, result StepResult) error {
	human, err := s.GetHumanStepContext(ctx, taskID, stepID)
	if err != nil {
		return err
	}
	if human.Run.Status != model.StepRunAwaiting && human.Run.Status != model.StepRunSucceeded {
		return fmt.Errorf("%w: 步骤 %s 不是等待中的人工 Gate", port.ErrInvalidTransition, stepID)
	}
	outputs, err := validateOutputs(human.Definition, result)
	if err != nil {
		return err
	}
	for i := range outputs {
		outputs[i].StepRunID = human.Run.ID
	}
	if human.Run.Status == model.StepRunSucceeded {
		execution, err := s.repo.GetTaskExecution(ctx, taskID)
		if err != nil {
			return err
		}
		if !sameStepOutputs(human.Run.ID, outputs, execution.StepOutputs) {
			return fmt.Errorf("%w: 人工 Gate 已由不同审核结果完成", port.ErrInvalidTransition)
		}
		return s.scheduler.Schedule(ctx, taskID)
	}
	if err := s.repo.CompleteAwaitingStep(ctx, human.Run.ID, outputs); err != nil {
		if errors.Is(err, port.ErrInvalidTransition) {
			execution, readErr := s.repo.GetTaskExecution(ctx, taskID)
			if readErr == nil && sameStepOutputs(human.Run.ID, outputs, execution.StepOutputs) {
				return s.scheduler.Schedule(ctx, taskID)
			}
		}
		return err
	}
	return s.scheduler.Schedule(ctx, taskID)
}

// ApprovePassThrough 处理不改写资源的通用人工 Gate。调用方只声明输出端口应放行
// 哪个受信输入端口，不能提交资源 ID，因此审核接口不会成为资源边界绕过通道。
func (s *RuntimeService) ApprovePassThrough(ctx context.Context, taskID, stepID string,
	outputInputs map[string]string) error {
	human, err := s.GetHumanStepContext(ctx, taskID, stepID)
	if err != nil {
		return err
	}
	if len(outputInputs) == 0 && len(human.Definition.Inputs) == 1 && len(human.Definition.Outputs) == 1 {
		for output := range human.Definition.Outputs {
			for input := range human.Definition.Inputs {
				outputInputs = map[string]string{output: input}
			}
		}
	}
	if len(outputInputs) != len(human.Definition.Outputs) {
		return fmt.Errorf("人工 Gate 必须为每个输出声明一个受信输入")
	}
	outputs := make(map[string]model.ResourceRef, len(outputInputs))
	for outputPort, inputPort := range outputInputs {
		expectedType, exists := human.Definition.Outputs[outputPort]
		if !exists {
			return fmt.Errorf("人工 Gate 不存在输出端口 %s", outputPort)
		}
		input, exists := human.Inputs[inputPort]
		if !exists {
			return fmt.Errorf("人工 Gate 不存在输入端口 %s", inputPort)
		}
		if input.ResourceType != expectedType {
			return fmt.Errorf("人工 Gate %s 输出类型 %s 与输入 %s 类型 %s 不一致",
				outputPort, expectedType, inputPort, input.ResourceType)
		}
		outputs[outputPort] = input
	}
	return s.ApproveHumanStep(ctx, taskID, stepID, StepResult{Outputs: outputs})
}

func sameStepOutputs(stepRunID string, expected, actual []model.StepResourceBinding) bool {
	byPort := make(map[string]model.StepResourceBinding)
	for _, binding := range actual {
		if binding.StepRunID == stepRunID {
			byPort[binding.PortName] = binding
		}
	}
	if len(byPort) != len(expected) {
		return false
	}
	for _, binding := range expected {
		stored, ok := byPort[binding.PortName]
		if !ok || stored.ResourceType != binding.ResourceType || stored.ResourceID != binding.ResourceID ||
			!equalJSONBoundary(stored.Boundary, binding.Boundary) {
			return false
		}
	}
	return true
}

func equalJSONBoundary(left, right json.RawMessage) bool {
	var a, b any
	if len(bytes.TrimSpace(left)) == 0 {
		left = json.RawMessage(`{}`)
	}
	if len(bytes.TrimSpace(right)) == 0 {
		right = json.RawMessage(`{}`)
	}
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func (s *RuntimeService) Get(ctx context.Context, taskID string) (*model.TaskExecution, error) {
	return s.repo.GetTaskExecution(ctx, taskID)
}
