package workflow

import (
	"context"
	"fmt"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type ManualExecutor struct {
	ref   domain.CapabilityRef
	label string
}

func NewManualExecutor(ref domain.CapabilityRef, label string) (*ManualExecutor, error) {
	if ref.Kind == "" || ref.Version < 1 {
		return nil, fmt.Errorf("人工 Capability 引用非法")
	}
	return &ManualExecutor{ref: ref, label: label}, nil
}

func (e *ManualExecutor) Capability() domain.CapabilityRef { return e.ref }

func (e *ManualExecutor) Execute(_ context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.await(execution), nil
}

func (e *ManualExecutor) Resume(_ context.Context, execution port.WorkflowCapabilityExecution) (port.WorkflowCapabilityResult, error) {
	return e.await(execution), nil
}

func (e *ManualExecutor) await(execution port.WorkflowCapabilityExecution) port.WorkflowCapabilityResult {
	message := e.label
	if message == "" {
		message = e.ref.Kind
	}
	return port.WorkflowCapabilityResult{Status: domain.NodeAwaitingManualCompletion, Code: "needs_human",
		Message: message + "需要人工完成", Question: &domain.HumanQuestion{ID: execution.NodeRunID,
			Path: "workflow.nodes." + execution.Node.ID, Prompt: message + "需要人工提交产物"}}
}
