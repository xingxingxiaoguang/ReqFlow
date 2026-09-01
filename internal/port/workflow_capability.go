package port

import (
	"context"
	"encoding/json"

	domain "reqflow/internal/domain/workflow"
)

type WorkflowCheckpointWriter interface {
	Save(ctx context.Context, checkpoint json.RawMessage) error
}

type WorkflowProgressReporter interface {
	Report(ctx context.Context, progress json.RawMessage) error
}

type WorkflowCapabilityExecution struct {
	WorkspaceID      string
	RunID            string
	NodeRunID        string
	Attempt          int
	Node             domain.ResolvedNode
	Rules            domain.RuleBundle
	Inputs           []domain.NodeResourceBinding
	Checkpoint       json.RawMessage
	CheckpointWriter WorkflowCheckpointWriter
	Progress         WorkflowProgressReporter
}

type WorkflowCapabilityResult struct {
	Outputs  []domain.NodeResourceBinding
	Status   domain.NodeRunStatus
	Code     string
	Message  string
	Question *domain.HumanQuestion
	Metrics  map[string]any
}

type WorkflowCapabilityExecutor interface {
	Capability() domain.CapabilityRef
	Execute(ctx context.Context, execution WorkflowCapabilityExecution) (WorkflowCapabilityResult, error)
	Resume(ctx context.Context, execution WorkflowCapabilityExecution) (WorkflowCapabilityResult, error)
}

// WorkflowSampleValue 是 Preview 顺序执行中上游节点暴露给下游的 temporary
// 样本载荷。它只存在于本次预览的 OutputManifest，不是正式资源。
type WorkflowSampleValue struct {
	NodeID       string
	Port         string
	ResourceType domain.ResourceType
	ResourceID   string
	Simulated    bool
	Payload      json.RawMessage
}

// WorkflowSampleReader 向 dry-run 执行器暴露当前节点的输入样本：Upstream
// 返回上游节点产物中连到该端口的样本；Explicit 返回预览 input 中为该节点
// 端口显式提供的人工模拟样本。
type WorkflowSampleReader interface {
	Upstream(port string) (WorkflowSampleValue, bool)
	Explicit(port string) (json.RawMessage, bool)
}

type WorkflowDryRunExecution struct {
	WorkspaceID string
	PreviewID   string
	Node        domain.ResolvedNode
	Rules       domain.RuleBundle
	Inputs      []domain.NodeResourceBinding
	Samples     WorkflowSampleReader
}

type WorkflowDryRunResult struct {
	// Outputs 是 temporary 资源绑定；ResourceID 由 dry-run 生成
	// （临时资源用 domain.TemporaryResourceID），不得指向正式资源表行。
	Outputs []domain.NodeResourceBinding
	// Samples 按输出端口携带完整样本载荷，供下游节点在内存中继续执行。
	Samples map[string]json.RawMessage
	// Simulated 声明本节点输出是模拟结果（人工样本/未落库的副作用计划）。
	Simulated bool
	Metrics   map[string]any
}

// WorkflowDryRunner 是 Capability 的预览执行合同。实现可以读取正式资源做
// 只读校验，但必须把全部输出留在预览样本里，不得写入任何正式资源表。
type WorkflowDryRunner interface {
	Capability() domain.CapabilityRef
	DryRun(ctx context.Context, execution WorkflowDryRunExecution) (WorkflowDryRunResult, error)
}

type WorkflowManualExecution struct {
	WorkspaceID string
	RunID       string
	NodeRunID   string
	Attempt     int
	Node        domain.ResolvedNode
	Rules       domain.RuleBundle
	Inputs      []domain.NodeResourceBinding
	Actor       string
	// Payload 是该 Capability 的领域载荷；HTTP 只负责透传，actor/workspace
	// 一律由服务端上下文注入。
	Payload json.RawMessage
}

// WorkflowManualCompleter 按 Capability 处理人工完成：读取节点真实输入、
// 校验领域载荷、生产正式资源，并返回服务端生成的输出绑定。
type WorkflowManualCompleter interface {
	Capability() domain.CapabilityRef
	Complete(ctx context.Context, execution WorkflowManualExecution) ([]domain.NodeResourceBinding, error)
}
