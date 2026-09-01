package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// DryRunRegistry 按 CapabilityRef 注册 Preview dry-run 执行器，与运行时
// NodeExecutorRegistry 保持同一注册规则。
type DryRunRegistry struct {
	items map[string]port.WorkflowDryRunner
}

func NewDryRunRegistry(runners ...port.WorkflowDryRunner) (*DryRunRegistry, error) {
	registry := &DryRunRegistry{items: map[string]port.WorkflowDryRunner{}}
	for _, runner := range runners {
		if runner == nil {
			return nil, fmt.Errorf("不能注册 nil WorkflowDryRunner")
		}
		ref := runner.Capability()
		key := capabilityKey(ref)
		if ref.Kind == "" || ref.Version < 1 || registry.items[key] != nil {
			return nil, fmt.Errorf("WorkflowDryRunner 引用非法或重复: %s", key)
		}
		registry.items[key] = runner
	}
	return registry, nil
}

func (r *DryRunRegistry) Lookup(ref domain.CapabilityRef) (port.WorkflowDryRunner, bool) {
	if r == nil {
		return nil, false
	}
	runner, ok := r.items[capabilityKey(ref)]
	return runner, ok
}

// ManualCompletionRegistry 按 CapabilityRef 注册人工完成处理器；未注册的
// 人工节点不接受任何客户端提交的产物。
type ManualCompletionRegistry struct {
	items map[string]port.WorkflowManualCompleter
}

func NewManualCompletionRegistry(completers ...port.WorkflowManualCompleter) (*ManualCompletionRegistry, error) {
	registry := &ManualCompletionRegistry{items: map[string]port.WorkflowManualCompleter{}}
	for _, completer := range completers {
		if completer == nil {
			return nil, fmt.Errorf("不能注册 nil WorkflowManualCompleter")
		}
		ref := completer.Capability()
		key := capabilityKey(ref)
		if ref.Kind == "" || ref.Version < 1 || registry.items[key] != nil {
			return nil, fmt.Errorf("WorkflowManualCompleter 引用非法或重复: %s", key)
		}
		registry.items[key] = completer
	}
	return registry, nil
}

func (r *ManualCompletionRegistry) Lookup(ref domain.CapabilityRef) (port.WorkflowManualCompleter, bool) {
	if r == nil {
		return nil, false
	}
	completer, ok := r.items[capabilityKey(ref)]
	return completer, ok
}

const maxPreviewSampleBytes = 64 << 10

type previewResourceRef struct {
	ResourceID string          `json:"resource_id"`
	Boundary   json.RawMessage `json:"boundary,omitempty"`
}

// previewInput 是 Preview/Acceptance 的统一输入合同：inputs 引用正式资源
// （只读），samples 为 LLM/人工节点提供显式人工模拟输出。
type previewInput struct {
	Inputs  map[string]previewResourceRef         `json:"inputs,omitempty"`
	Samples map[string]map[string]json.RawMessage `json:"samples,omitempty"`
}

func decodePreviewInput(raw json.RawMessage) (previewInput, error) {
	var input previewInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return input, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return previewInput{}, fmt.Errorf("预览 input 必须是只含 inputs/samples 的 JSON object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return previewInput{}, fmt.Errorf("预览 input 只能包含一个 JSON 值")
	}
	for name, ref := range input.Inputs {
		if strings.TrimSpace(ref.ResourceID) == "" {
			return previewInput{}, fmt.Errorf("预览输入 %s 缺少 resource_id", name)
		}
	}
	for nodeID, ports := range input.Samples {
		if strings.TrimSpace(nodeID) == "" || len(ports) == 0 {
			return previewInput{}, fmt.Errorf("预览 samples 引用了空节点或空端口")
		}
		for port := range ports {
			if strings.TrimSpace(port) == "" {
				return previewInput{}, fmt.Errorf("预览 samples 节点 %s 存在空端口", nodeID)
			}
		}
	}
	return input, nil
}

type manifestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type manifestOutput struct {
	ResourceType domain.ResourceType `json:"resource_type"`
	ResourceID   string              `json:"resource_id"`
	Temporary    bool                `json:"temporary"`
	Metrics      map[string]any      `json:"metrics,omitempty"`
	Sample       json.RawMessage     `json:"sample,omitempty"`
}

type manifestNode struct {
	Capability string                    `json:"capability"`
	Name       string                    `json:"name"`
	Status     string                    `json:"status"`
	Simulated  bool                      `json:"simulated,omitempty"`
	Error      *manifestError            `json:"error,omitempty"`
	Outputs    map[string]manifestOutput `json:"outputs,omitempty"`
}

type previewManifest struct {
	Temporary bool                     `json:"temporary"`
	Nodes     map[string]*manifestNode `json:"nodes"`
}

// previewEngine 顺序执行 Draft 的 ResolvedNode。所有输出都是 temporary：
// 确定性 Capability 运行真实内核，LLM/人工节点消费显式样本并标记模拟。
type previewEngine struct {
	previewID string
	nodes     []domain.ResolvedNode
	registry  *DryRunRegistry
	input     previewInput
	draft     domain.WorkflowDraft
	samples   map[string]port.WorkflowSampleValue
}

func newPreviewEngine(previewID string, draft domain.WorkflowDraft, catalog domain.CapabilityCatalog,
	registry *DryRunRegistry, input previewInput) (*previewEngine, error) {
	order, err := domain.LinearOrder(draft)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.WorkflowNode, len(draft.Nodes))
	for _, node := range draft.Nodes {
		byID[node.ID] = node
	}
	resolved := make([]domain.ResolvedNode, 0, len(order))
	for _, id := range order {
		node := byID[id]
		definition, exists := catalog.Lookup(node.Capability)
		if !exists {
			return nil, fmt.Errorf("capability %s 在预览时消失", capabilityKey(node.Capability))
		}
		config := node.Config
		if len(config) == 0 {
			config = definition.DefaultConfig
		}
		resolved = append(resolved, domain.ResolvedNode{ID: node.ID, Name: node.Name,
			Capability: definition, Config: append(json.RawMessage(nil), config...)})
	}
	return &previewEngine{previewID: previewID, nodes: resolved,
		registry: registry, input: input, draft: draft,
		samples: map[string]port.WorkflowSampleValue{}}, nil
}

type nodeSampleReader struct {
	engine *previewEngine
	node   domain.ResolvedNode
}

func (r nodeSampleReader) Upstream(portName string) (port.WorkflowSampleValue, bool) {
	for _, connection := range r.engine.draft.Connections {
		if connection.To.Kind != domain.EndpointNodeInput || connection.To.NodeID != r.node.ID ||
			connection.To.Port != portName || connection.From.Kind != domain.EndpointNodeOutput {
			continue
		}
		value, ok := r.engine.samples[connection.From.NodeID+"\x00"+connection.From.Port]
		return value, ok
	}
	return port.WorkflowSampleValue{}, false
}

func (r nodeSampleReader) Explicit(portName string) (json.RawMessage, bool) {
	ports := r.engine.input.Samples[r.node.ID]
	if ports == nil {
		return nil, false
	}
	payload, ok := ports[portName]
	return payload, ok
}

// nodeInputs 把流程输入引用与上游 temporary 输出绑定成节点输入。
func (e *previewEngine) nodeInputs(node domain.ResolvedNode) []domain.NodeResourceBinding {
	inputs := make([]domain.NodeResourceBinding, 0, len(node.Capability.Inputs))
	for _, connection := range e.draft.Connections {
		if connection.To.Kind != domain.EndpointNodeInput || connection.To.NodeID != node.ID {
			continue
		}
		binding := domain.NodeResourceBinding{NodeID: node.ID, Port: connection.To.Port,
			Direction: domain.BindingInput, Provenance: json.RawMessage(`{"producer":"preview"}`)}
		if connection.From.Kind == domain.EndpointWorkflowInput {
			ref := e.input.Inputs[connection.From.Port]
			workflowPort, ok := findWorkflowInput(e.draft.Inputs, connection.From.Port)
			if ok {
				binding.ResourceType = workflowPort.ResourceType
			}
			binding.ResourceID = ref.ResourceID
			binding.Boundary = append(json.RawMessage(nil), ref.Boundary...)
			binding.Provenance = json.RawMessage(`{"producer":"workflow_input","preview":true}`)
		} else if sample, ok := e.samples[connection.From.NodeID+"\x00"+connection.From.Port]; ok {
			binding.ResourceType = sample.ResourceType
			binding.ResourceID = sample.ResourceID
		}
		inputs = append(inputs, binding)
	}
	return inputs
}

// run 顺序执行所有节点；任一节点失败即停止，剩余节点标记 skipped。
func (e *previewEngine) run(ctx context.Context) (*previewManifest, []domain.ValidationIssue) {
	manifest := &previewManifest{Temporary: true, Nodes: map[string]*manifestNode{}}
	issues := []domain.ValidationIssue{}
	stopped := false
	for _, node := range e.nodes {
		entry := &manifestNode{Capability: capabilityKey(node.Capability.Ref), Name: node.Name}
		manifest.Nodes[node.ID] = entry
		if stopped {
			entry.Status = "skipped"
			continue
		}
		runner, ok := e.registry.Lookup(node.Capability.Ref)
		if !ok {
			entry.Status, entry.Error = "failed", &manifestError{Code: "dry_run_not_registered",
				Message: fmt.Sprintf("Capability %s 未注册预览执行器", capabilityKey(node.Capability.Ref))}
			issues = append(issues, domain.ValidationIssue{Code: "preview_node_failed",
				Path: "nodes." + node.ID, Message: entry.Error.Message, Severity: domain.SeverityError})
			stopped = true
			continue
		}
		result, err := runner.DryRun(ctx, port.WorkflowDryRunExecution{WorkspaceID: e.draft.WorkspaceID,
			PreviewID: e.previewID, Node: node, Rules: e.draft.Rules,
			Inputs: e.nodeInputs(node), Samples: nodeSampleReader{engine: e, node: node}})
		if err != nil {
			entry.Status, entry.Error = "failed", &manifestError{Code: "dry_run_failed", Message: err.Error()}
			issues = append(issues, domain.ValidationIssue{Code: "preview_node_failed",
				Path: "nodes." + node.ID, Message: err.Error(), Severity: domain.SeverityError})
			stopped = true
			continue
		}
		if err := validateResolvedNodeOutputs(node, result.Outputs); err != nil {
			entry.Status, entry.Error = "failed", &manifestError{Code: "dry_run_outputs_invalid", Message: err.Error()}
			issues = append(issues, domain.ValidationIssue{Code: "preview_node_failed",
				Path: "nodes." + node.ID, Message: err.Error(), Severity: domain.SeverityError})
			stopped = true
			continue
		}
		entry.Status, entry.Simulated, entry.Outputs = "succeeded", result.Simulated, map[string]manifestOutput{}
		for _, output := range result.Outputs {
			entry.Outputs[output.Port] = manifestOutput{ResourceType: output.ResourceType,
				ResourceID: output.ResourceID, Temporary: true, Metrics: result.Metrics,
				Sample: boundSample(result.Samples[output.Port])}
			e.samples[node.ID+"\x00"+output.Port] = port.WorkflowSampleValue{NodeID: node.ID,
				Port: output.Port, ResourceType: output.ResourceType, ResourceID: output.ResourceID,
				Simulated: result.Simulated, Payload: result.Samples[output.Port]}
		}
	}
	return manifest, issues
}

// boundSample 限制嵌入 manifest 的样本体积；下游节点仍消费完整内存样本。
func boundSample(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) <= maxPreviewSampleBytes {
		return append(json.RawMessage(nil), payload...)
	}
	truncated, err := json.Marshal(map[string]any{"_truncated": true, "size_bytes": len(payload)})
	if err != nil {
		return nil
	}
	return truncated
}

// validateResolvedNodeOutputs 校验 dry-run 输出与 Capability 端口合同一致。
// 资源只落入本次预览的 manifest JSON，dry-run 本身不产生任何正式资源行。
func validateResolvedNodeOutputs(node domain.ResolvedNode, outputs []domain.NodeResourceBinding) error {
	seen := map[string]bool{}
	for _, output := range outputs {
		if output.Direction != "" && output.Direction != domain.BindingOutput {
			return fmt.Errorf("预览产物方向非法")
		}
		definition, ok := findPort(node.Capability.Outputs, output.Port)
		if !ok {
			return fmt.Errorf("预览产物端口 %s 不存在", output.Port)
		}
		if definition.ResourceType != output.ResourceType {
			return fmt.Errorf("预览产物 %s 类型不匹配", output.Port)
		}
		if strings.TrimSpace(output.ResourceID) == "" {
			return fmt.Errorf("预览产物 %s 缺少资源 ID", output.Port)
		}
		if seen[output.Port] {
			return fmt.Errorf("预览产物端口 %s 重复", output.Port)
		}
		seen[output.Port] = true
	}
	for _, definition := range node.Capability.Outputs {
		if definition.Required && !seen[definition.Name] {
			return fmt.Errorf("预览必填产物 %s 缺失", definition.Name)
		}
	}
	return nil
}
