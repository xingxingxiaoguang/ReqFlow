package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"reqflow/internal/domain/model"
)

var ErrExecutorNotRegistered = errors.New("step executor not registered")

// CheckpointWriter 和 ProgressReporter 的实现必须用 StepRunID + lease owner 做条件写入。
// Executor 不持有仓储，也不需要知道 lease 细节。
type CheckpointWriter interface {
	Save(ctx context.Context, checkpoint json.RawMessage) error
}

type ProgressReporter interface {
	Report(ctx context.Context, progress json.RawMessage) error
}

// StepRunContext 是 Executor 的完整、稳定输入。Inputs 已由 Orchestrator 从任务输入
// 和前置步骤输出解析完成；IdempotencyKey 可直接传给外部写入端。
type StepRunContext struct {
	TaskID     string
	StepRunID  string
	StepID     string
	Attempt    int
	InputHash  string
	ConfigHash string
	// IdempotencyKey 对外部副作用保持跨 attempt 稳定；ExecutionKey 唯一标识本次尝试。
	IdempotencyKey string
	ExecutionKey   string
	Inputs         map[string]model.ResourceRef
	Config         json.RawMessage
	Checkpoint     CheckpointWriter
	Progress       ProgressReporter
}

type StepResult struct {
	Outputs map[string]model.ResourceRef
	Metrics map[string]any
}

type StepExecutor interface {
	Kind() model.StepKind
	ValidateDefinition(ctx context.Context, def model.StepDefinition) error
	Execute(ctx context.Context, run StepRunContext) (StepResult, error)
	Resume(ctx context.Context, run StepRunContext, checkpoint json.RawMessage) (StepResult, error)
}

// Registry 按 Kind 唯一注册 Executor。human.review 是 Orchestrator 内建 Gate，
// 不允许伪装成普通 Worker Executor。
type Registry struct {
	executors map[model.StepKind]StepExecutor
}

func NewRegistry(executors ...StepExecutor) (*Registry, error) {
	r := &Registry{executors: make(map[model.StepKind]StepExecutor, len(executors))}
	for _, executor := range executors {
		if executor == nil {
			return nil, fmt.Errorf("注册了 nil Executor")
		}
		kind := executor.Kind()
		if strings.TrimSpace(string(kind)) == "" {
			return nil, fmt.Errorf("Executor Kind 不能为空")
		}
		if kind == model.StepKindHumanReview {
			return nil, fmt.Errorf("%s 是内建人工 Gate，不能注册 Worker Executor", kind)
		}
		if _, exists := r.executors[kind]; exists {
			return nil, fmt.Errorf("Executor Kind 重复注册: %s", kind)
		}
		r.executors[kind] = executor
	}
	return r, nil
}

func (r *Registry) Lookup(kind model.StepKind) (StepExecutor, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: %s", ErrExecutorNotRegistered, kind)
	}
	executor, ok := r.executors[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrExecutorNotRegistered, kind)
	}
	return executor, nil
}

func (r *Registry) ValidateDefinition(ctx context.Context, definition model.TaskDefinition) error {
	for _, step := range definition.Steps {
		if step.Kind == model.StepKindHumanReview {
			continue
		}
		executor, err := r.Lookup(step.Kind)
		if err != nil {
			return fmt.Errorf("步骤 %s: %w", step.ID, err)
		}
		// 同 Kind 可以重复出现；每个步骤的 config 仍需分别校验。
		if err := executor.ValidateDefinition(ctx, step); err != nil {
			return fmt.Errorf("步骤 %s 的 Executor 配置非法: %w", step.ID, err)
		}
	}
	return nil
}

func (r *Registry) Kinds() []model.StepKind {
	out := make([]model.StepKind, 0, len(r.executors))
	for kind := range r.executors {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
