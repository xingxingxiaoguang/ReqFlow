package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// Scheduler 只根据数据库快照计算状态迁移。它不执行步骤，也不在内存保存任务状态；
// 因此可在启动、Worker 完成后和周期性对账时重复调用。
type Scheduler struct {
	repo port.OrchestratorSchedulerRepo
}

func NewScheduler(repo port.OrchestratorSchedulerRepo) *Scheduler {
	return &Scheduler{repo: repo}
}

func (s *Scheduler) Schedule(ctx context.Context, taskID string) error {
	execution, err := s.repo.GetTaskExecution(ctx, taskID)
	if err != nil {
		return err
	}
	if execution.Task.Status != model.TaskStatusRunning {
		return nil
	}
	definition, err := executionDefinition(execution)
	if err != nil {
		return err
	}

	runs := make(map[string]model.StepRun, len(execution.Steps))
	allDone := len(execution.Steps) == len(definition.Steps)
	for _, run := range execution.Steps {
		runs[run.StepID] = run
		if run.Status != model.StepRunSucceeded && run.Status != model.StepRunSkipped {
			allDone = false
		}
	}
	if allDone {
		outputs, err := resolveTaskOutputs(definition, execution)
		if err != nil {
			return err
		}
		return s.repo.CompleteTask(ctx, taskID, outputs)
	}

	queued := make([]port.StepQueueEntry, 0)
	awaiting := make([]port.StepQueueEntry, 0)
	for _, step := range definition.Steps {
		run, ok := runs[step.ID]
		if !ok {
			return fmt.Errorf("任务 %s 缺少 StepRun %s", taskID, step.ID)
		}
		if run.Kind != step.Kind {
			return fmt.Errorf("StepRun %s kind=%s 与定义快照 %s 不一致", step.ID, run.Kind, step.Kind)
		}
		if run.Status != model.StepRunPending || !dependenciesSucceeded(step, runs) {
			continue
		}
		inputs, err := resolveStepInputs(step, execution)
		if err != nil {
			return err
		}
		entry := port.StepQueueEntry{StepRunID: run.ID, InputHash: resourceInputHash(inputs)}
		if step.Kind == model.StepKindHumanReview {
			awaiting = append(awaiting, entry)
		} else {
			queued = append(queued, entry)
		}
	}
	// 即使本轮没有新 ready step，也让仓储按实际 queued/running/awaiting 数量
	// 收敛任务级状态，覆盖“并行分支只剩人工 Gate”的场景。
	return s.repo.QueueReadySteps(ctx, taskID, queued, awaiting)
}

func (s *Scheduler) Reconcile(ctx context.Context, limit int) error {
	ids, err := s.repo.ListSchedulableTaskIDs(ctx, limit)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.Schedule(ctx, id); err != nil {
			return fmt.Errorf("调度任务 %s: %w", id, err)
		}
	}
	return nil
}

func executionDefinition(execution *model.TaskExecution) (model.TaskDefinition, error) {
	var definition model.TaskDefinition
	if execution == nil || strings.TrimSpace(execution.Task.DefinitionSnapshot) == "" {
		return definition, fmt.Errorf("任务缺少 definition snapshot")
	}
	if err := json.Unmarshal([]byte(execution.Task.DefinitionSnapshot), &definition); err != nil {
		return definition, fmt.Errorf("解析 definition snapshot: %w", err)
	}
	if err := logic.ValidateTaskDefinition(definition); err != nil {
		return definition, fmt.Errorf("definition snapshot 非法: %w", err)
	}
	return definition, nil
}

func dependenciesSucceeded(step model.StepDefinition, runs map[string]model.StepRun) bool {
	for _, dependency := range step.DependsOn {
		run, ok := runs[dependency]
		if !ok || (run.Status != model.StepRunSucceeded && run.Status != model.StepRunSkipped) {
			return false
		}
	}
	return true
}

func resolveStepInputs(step model.StepDefinition, execution *model.TaskExecution) (map[string]model.ResourceRef, error) {
	taskInputs := make(map[string]model.ResourceRef)
	for _, binding := range execution.Inputs {
		if binding.Direction != model.ResourceInput {
			continue
		}
		taskInputs[binding.PortName] = model.ResourceRef{
			ResourceType: binding.ResourceType, ResourceID: binding.ResourceID, Boundary: binding.Boundary,
		}
	}
	runByStep := make(map[string]string, len(execution.Steps))
	for _, run := range execution.Steps {
		runByStep[run.StepID] = run.ID
	}
	stepOutputs := make(map[string]model.ResourceRef)
	for _, binding := range execution.StepOutputs {
		key := binding.StepRunID + "." + binding.PortName
		stepOutputs[key] = model.ResourceRef{
			ResourceType: binding.ResourceType, ResourceID: binding.ResourceID, Boundary: binding.Boundary,
		}
	}

	resolved := make(map[string]model.ResourceRef, len(step.Inputs))
	for portName, ref := range step.Inputs {
		resource, err := resolveReference(ref, taskInputs, runByStep, stepOutputs)
		if err != nil {
			return nil, fmt.Errorf("步骤 %s 输入 %s: %w", step.ID, portName, err)
		}
		resolved[portName] = resource
	}
	return resolved, nil
}

func resolveTaskOutputs(definition model.TaskDefinition, execution *model.TaskExecution) ([]model.TaskResourceBinding, error) {
	taskInputs := make(map[string]model.ResourceRef)
	for _, binding := range execution.Inputs {
		if binding.Direction == model.ResourceInput {
			taskInputs[binding.PortName] = model.ResourceRef{
				ResourceType: binding.ResourceType, ResourceID: binding.ResourceID, Boundary: binding.Boundary,
			}
		}
	}
	runByStep := make(map[string]string, len(execution.Steps))
	for _, run := range execution.Steps {
		runByStep[run.StepID] = run.ID
	}
	stepOutputs := make(map[string]model.ResourceRef, len(execution.StepOutputs))
	for _, binding := range execution.StepOutputs {
		stepOutputs[binding.StepRunID+"."+binding.PortName] = model.ResourceRef{
			ResourceType: binding.ResourceType, ResourceID: binding.ResourceID, Boundary: binding.Boundary,
		}
	}
	out := make([]model.TaskResourceBinding, 0, len(definition.OutputBindings))
	portNames := make([]string, 0, len(definition.OutputBindings))
	for portName := range definition.OutputBindings {
		portNames = append(portNames, portName)
	}
	sort.Strings(portNames)
	for _, portName := range portNames {
		ref := definition.OutputBindings[portName]
		resource, err := resolveReference(ref, taskInputs, runByStep, stepOutputs)
		if err != nil {
			return nil, fmt.Errorf("任务输出 %s: %w", portName, err)
		}
		expected := definition.OutputPorts[portName].ResourceType
		if resource.ResourceType != expected {
			return nil, fmt.Errorf("任务输出 %s 需要 %s，实际为 %s", portName, expected, resource.ResourceType)
		}
		out = append(out, model.TaskResourceBinding{PortName: portName, Direction: model.ResourceOutput,
			ResourceType: resource.ResourceType, ResourceID: resource.ResourceID, Boundary: resource.Boundary})
	}
	return out, nil
}

func resourceInputHash(inputs map[string]model.ResourceRef) string {
	canonical, _ := json.Marshal(inputs)
	sum := sha256.Sum256(canonical)
	return fmt.Sprintf("%x", sum[:])
}

func resolveReference(ref string, taskInputs map[string]model.ResourceRef, runByStep map[string]string, stepOutputs map[string]model.ResourceRef) (model.ResourceRef, error) {
	if strings.HasPrefix(ref, "$task.") {
		portName := strings.TrimPrefix(ref, "$task.")
		resource, ok := taskInputs[portName]
		if !ok {
			return model.ResourceRef{}, fmt.Errorf("输入资源 %s 未绑定", ref)
		}
		return resource, nil
	}
	if strings.HasPrefix(ref, "$step.") {
		parts := strings.Split(strings.TrimPrefix(ref, "$step."), ".")
		if len(parts) != 2 {
			return model.ResourceRef{}, fmt.Errorf("步骤资源引用非法: %s", ref)
		}
		runID, ok := runByStep[parts[0]]
		if !ok {
			return model.ResourceRef{}, fmt.Errorf("步骤 %s 不存在", parts[0])
		}
		resource, ok := stepOutputs[runID+"."+parts[1]]
		if !ok {
			return model.ResourceRef{}, fmt.Errorf("步骤输出 %s 尚未绑定", ref)
		}
		return resource, nil
	}
	return model.ResourceRef{}, fmt.Errorf("未知资源引用: %s", ref)
}

func validateOutputs(step model.StepDefinition, result StepResult) ([]model.StepResourceBinding, error) {
	if len(result.Outputs) != len(step.Outputs) {
		return nil, fmt.Errorf("步骤 %s 应产出 %d 个端口，实际为 %d", step.ID, len(step.Outputs), len(result.Outputs))
	}
	out := make([]model.StepResourceBinding, 0, len(step.Outputs))
	for portName, expected := range step.Outputs {
		resource, ok := result.Outputs[portName]
		if !ok {
			return nil, fmt.Errorf("步骤 %s 缺少输出端口 %s", step.ID, portName)
		}
		if resource.ResourceType != expected {
			return nil, fmt.Errorf("步骤 %s 输出 %s 需要 %s，实际为 %s", step.ID, portName, expected, resource.ResourceType)
		}
		if strings.TrimSpace(resource.ResourceID) == "" {
			return nil, fmt.Errorf("步骤 %s 输出 %s 缺少 resource_id", step.ID, portName)
		}
		out = append(out, model.StepResourceBinding{PortName: portName, ResourceType: resource.ResourceType,
			ResourceID: resource.ResourceID, Boundary: resource.Boundary})
	}
	return out, nil
}
