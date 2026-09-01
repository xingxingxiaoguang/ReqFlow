package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	baseagent "reqflow/internal/app/agent"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type NodeCheckpointWriter = port.WorkflowCheckpointWriter
type NodeProgressReporter = port.WorkflowProgressReporter
type NodeExecution = port.WorkflowCapabilityExecution
type NodeExecutionResult = port.WorkflowCapabilityResult
type WorkflowNodeExecutor = port.WorkflowCapabilityExecutor

type NodeExecutorRegistry struct {
	items map[string]WorkflowNodeExecutor
}

func NewNodeExecutorRegistry(executors ...WorkflowNodeExecutor) (*NodeExecutorRegistry, error) {
	registry := &NodeExecutorRegistry{items: map[string]WorkflowNodeExecutor{}}
	for _, executor := range executors {
		if executor == nil {
			return nil, fmt.Errorf("不能注册 nil WorkflowNodeExecutor")
		}
		ref := executor.Capability()
		key := capabilityKey(ref)
		if ref.Kind == "" || ref.Version < 1 || registry.items[key] != nil {
			return nil, fmt.Errorf("WorkflowNodeExecutor 引用非法或重复: %s", key)
		}
		registry.items[key] = executor
	}
	return registry, nil
}

func (r *NodeExecutorRegistry) Lookup(ref domain.CapabilityRef) (WorkflowNodeExecutor, bool) {
	if r == nil {
		return nil, false
	}
	executor, ok := r.items[capabilityKey(ref)]
	return executor, ok
}

func capabilityKey(ref domain.CapabilityRef) string {
	return fmt.Sprintf("%s@%d", ref.Kind, ref.Version)
}

type RunInput struct {
	Port         string              `json:"port"`
	ResourceType domain.ResourceType `json:"resource_type"`
	ResourceID   string              `json:"resource_id"`
	Boundary     json.RawMessage     `json:"boundary,omitempty"`
}

type CreateRunRequest struct {
	RevisionID string     `json:"revision_id"`
	Inputs     []RunInput `json:"inputs"`
}

type RuntimeService struct {
	repo      port.WorkflowRunRepo
	revisions interface {
		GetRevision(context.Context, string) (*domain.WorkflowRevision, error)
	}
	registry *NodeExecutorRegistry
	lease    time.Duration
	retry    int
	now      func() time.Time
	active   sync.Map
}

func NewRuntimeService(repo port.WorkflowRunRepo,
	revisions interface {
		GetRevision(context.Context, string) (*domain.WorkflowRevision, error)
	},
	registry *NodeExecutorRegistry, lease time.Duration, retry int) (*RuntimeService, error) {
	if repo == nil || revisions == nil || registry == nil {
		return nil, fmt.Errorf("workflow runtime: repository, revision reader and executor registry are required")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if retry < 0 {
		retry = 2
	}
	return &RuntimeService{repo: repo, revisions: revisions, registry: registry, lease: lease, retry: retry, now: time.Now}, nil
}

func (s *RuntimeService) Create(ctx context.Context, request CreateRunRequest) (*domain.WorkflowRunSnapshot, error) {
	revision, err := s.revisions.GetRevision(ctx, request.RevisionID)
	if err != nil {
		return nil, err
	}
	inputs, err := resolveRunInputs(*revision, request.Inputs)
	if err != nil {
		return nil, err
	}
	run, nodes, err := domain.NewWorkflowRun(uuid.NewString(), *revision, inputs, s.now())
	if err != nil {
		return nil, err
	}
	for index := range inputs {
		if inputs[index].ID == "" {
			inputs[index].ID = uuid.NewString()
			inputs[index].CreatedAt = run.CreatedAt
		}
	}
	for index := range nodes {
		if strings.HasPrefix(nodes[index].ID, run.ID+"-node-") {
			nodes[index].ID = uuid.NewString()
		}
	}
	run.Inputs = inputs
	if err := s.repo.CreateWorkflowRun(ctx, *run, nodes); err != nil {
		return nil, err
	}
	return s.repo.GetWorkflowRun(ctx, run.ID)
}

func (s *RuntimeService) Get(ctx context.Context, id string) (*domain.WorkflowRunSnapshot, error) {
	return s.repo.GetWorkflowRun(ctx, id)
}

func (s *RuntimeService) List(ctx context.Context, workspaceID string, limit int) ([]domain.WorkflowRunSnapshot, error) {
	if strings.TrimSpace(workspaceID) == "" {
		workspaceID = LocalWorkspaceID
	}
	return s.repo.ListWorkflowRuns(ctx, workspaceID, limit)
}

func (s *RuntimeService) Start(ctx context.Context, id string) error {
	return s.repo.StartWorkflowRun(ctx, id)
}
func (s *RuntimeService) Pause(ctx context.Context, id string) error {
	if err := s.repo.RequestWorkflowRunPause(ctx, id); err != nil {
		return err
	}
	if value, ok := s.active.Load(id); ok {
		value.(context.CancelFunc)()
	}
	return nil
}
func (s *RuntimeService) Resume(ctx context.Context, id string) error {
	return s.repo.ResumeWorkflowRun(ctx, id)
}

func (s *RuntimeService) CompleteManual(ctx context.Context, runID, nodeID, actor string,
	outputs []domain.NodeResourceBinding) error {
	snapshot, err := s.repo.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	node, ok := findNodeRun(snapshot.Nodes, nodeID)
	if !ok || node.Status != domain.NodeAwaitingManualCompletion {
		return port.ErrRunInvalidTransition
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("人工完成必须包含提交者")
	}
	if err := validateNodeOutputs(node, outputs); err != nil {
		return err
	}
	for index := range outputs {
		outputs[index].NodeRunID = node.ID
		outputs[index].NodeID = node.NodeID
		outputs[index].Direction = domain.BindingOutput
		outputs[index].Provenance = json.RawMessage(fmt.Sprintf(`{"producer":"human","actor":%q}`, actor))
	}
	return s.repo.CompleteWorkflowNodeManual(ctx, node.ID, node.Attempt, actor, outputs)
}

func (s *RuntimeService) RunOnce(ctx context.Context, owner string) error {
	node, err := s.repo.ClaimWorkflowNode(ctx, owner, s.now().Add(s.lease))
	if err != nil {
		return err
	}
	snapshot, err := s.repo.GetWorkflowRun(ctx, node.RunID)
	if err != nil {
		return err
	}
	if snapshot.Run.Status == domain.RunPausing {
		return s.repo.PauseWorkflowNode(ctx, node.ID, node.Attempt, owner)
	}
	executor, ok := s.registry.Lookup(node.Node.Capability.Ref)
	if !ok {
		if node.Node.Capability.ManualCompletion {
			return s.repo.AwaitWorkflowNodeManual(ctx, node.ID, node.Attempt, owner,
				"executor_not_registered", fmt.Sprintf("Capability %s 当前不可自动执行", capabilityKey(node.Node.Capability.Ref)))
		}
		return s.failNode(ctx, node, owner, "executor_not_registered", fmt.Errorf("Capability %s 未注册", capabilityKey(node.Node.Capability.Ref)))
	}
	inputs, err := s.repo.GetNodeInputs(ctx, node.ID)
	if err != nil {
		return s.failNode(ctx, node, owner, "input_resolution_failed", err)
	}
	if err := validateNodeInputs(*node, inputs); err != nil {
		return s.failNode(ctx, node, owner, "invalid_node_inputs", err)
	}
	nodeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.active.Store(node.RunID, cancel)
	defer s.active.Delete(node.RunID)
	stopRenew := make(chan struct{})
	renewErr := make(chan error, 1)
	go s.renewNodeLease(nodeCtx, cancel, node.ID, node.Attempt, owner, stopRenew, renewErr)
	execution := NodeExecution{RunID: snapshot.Run.ID, NodeRunID: node.ID, Attempt: node.Attempt, Node: node.Node,
		Rules: snapshot.Run.Revision.Rules, Inputs: inputs, Checkpoint: node.Checkpoint,
		CheckpointWriter: ownedNodeCheckpoint{repo: s.repo, nodeRunID: node.ID, attempt: node.Attempt, owner: owner},
		Progress:         ownedNodeProgress{repo: s.repo, nodeRunID: node.ID, attempt: node.Attempt, owner: owner}}
	var result NodeExecutionResult
	if node.Attempt > 1 && hasJSONContent(node.Checkpoint) {
		result, err = executor.Resume(nodeCtx, execution)
	} else {
		result, err = executor.Execute(nodeCtx, execution)
	}
	close(stopRenew)
	if leaseErr := <-renewErr; leaseErr != nil {
		return leaseErr
	}
	if nodeCtx.Err() != nil && ctx.Err() == nil {
		return s.repo.PauseWorkflowNode(context.WithoutCancel(ctx), node.ID, node.Attempt, owner)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return s.handleNodeFailure(ctx, node, owner, "executor_failed", err)
	}
	if result.Status == domain.NodeAwaitingManualCompletion || result.Question != nil {
		message := result.Message
		if message == "" && result.Question != nil {
			message = result.Question.Prompt
		}
		return s.repo.AwaitWorkflowNodeManual(ctx, node.ID, node.Attempt, owner, result.Code, message)
	}
	if result.Status == domain.NodeFailed {
		return s.handleNodeFailure(ctx, node, owner, result.Code, errors.New(result.Message))
	}
	if err := validateNodeOutputs(*node, result.Outputs); err != nil {
		return s.handleNodeFailure(ctx, node, owner, "invalid_node_outputs", err)
	}
	for index := range result.Outputs {
		if result.Outputs[index].ID == "" {
			result.Outputs[index].ID = uuid.NewString()
		}
		result.Outputs[index].NodeRunID = node.ID
		result.Outputs[index].NodeID = node.NodeID
		result.Outputs[index].Direction = domain.BindingOutput
		if len(result.Outputs[index].Provenance) == 0 {
			result.Outputs[index].Provenance = json.RawMessage(`{"producer":"automated"}`)
		}
	}
	return s.repo.CompleteWorkflowNode(ctx, node.ID, node.Attempt, owner, result.Outputs)
}

func (s *RuntimeService) failNode(ctx context.Context, node *domain.NodeRun, owner, code string, err error) error {
	if err == nil {
		err = errors.New("unknown node failure")
	}
	return s.repo.FailWorkflowNode(ctx, node.ID, node.Attempt, owner, code, err.Error())
}

func (s *RuntimeService) handleNodeFailure(ctx context.Context, node *domain.NodeRun, owner, code string, err error) error {
	if err == nil {
		err = errors.New("unknown node failure")
	}
	if retryableNodeError(err) && node.Attempt <= s.retry {
		return s.repo.RetryWorkflowNode(ctx, node.ID, node.Attempt, owner, code, err.Error(), retryAt(s.now(), node.Attempt))
	}
	if node.Node.Capability.ManualCompletion {
		return s.repo.AwaitWorkflowNodeManual(ctx, node.ID, node.Attempt, owner, "provider_failed", err.Error())
	}
	return s.failNode(ctx, node, owner, code, err)
}

func (s *RuntimeService) Retry(ctx context.Context, runID, nodeID string) error {
	snapshot, err := s.repo.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}
	node, ok := findNodeRun(snapshot.Nodes, nodeID)
	if !ok {
		return port.ErrNodeRunNotFound
	}
	if node.Status != domain.NodeFailed {
		return port.ErrRunInvalidTransition
	}
	return s.repo.RequeueFailedWorkflowNode(ctx, node.ID)
}

func (s *RuntimeService) renewNodeLease(ctx context.Context, cancel context.CancelFunc, nodeID string, attempt int, owner string, stop <-chan struct{}, result chan<- error) {
	interval := s.lease / 3
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			result <- nil
			return
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			if err := s.repo.RenewWorkflowNodeLease(context.WithoutCancel(ctx), nodeID, attempt, owner, s.now().Add(s.lease)); err != nil {
				cancel()
				result <- err
				return
			}
		}
	}
}

type RuntimeWorker struct {
	service  *RuntimeService
	owner    string
	interval time.Duration
}

func NewRuntimeWorker(service *RuntimeService, owner string, interval time.Duration) (*RuntimeWorker, error) {
	if service == nil {
		return nil, fmt.Errorf("workflow runtime worker service is required")
	}
	if strings.TrimSpace(owner) == "" {
		owner = uuid.NewString()
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &RuntimeWorker{service: service, owner: owner, interval: interval}, nil
}

func (w *RuntimeWorker) Run(ctx context.Context) error {
	if _, err := w.service.repo.RecoverWorkflowNodeLeases(ctx); err != nil {
		return err
	}
	for {
		if err := w.service.RunOnce(ctx, w.owner); err != nil && !errors.Is(err, port.ErrNoRunnableNode) {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return err
		}
		timer := time.NewTimer(w.interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type ownedNodeCheckpoint struct {
	repo      port.WorkflowRunRepo
	nodeRunID string
	attempt   int
	owner     string
}

func (w ownedNodeCheckpoint) Save(ctx context.Context, value json.RawMessage) error {
	return w.repo.SaveWorkflowNodeCheckpoint(ctx, w.nodeRunID, w.attempt, w.owner, value)
}

type ownedNodeProgress struct {
	repo      port.WorkflowRunRepo
	nodeRunID string
	attempt   int
	owner     string
}

func (w ownedNodeProgress) Report(ctx context.Context, value json.RawMessage) error {
	return w.repo.SaveWorkflowNodeProgress(ctx, w.nodeRunID, w.attempt, w.owner, value)
}

func resolveRunInputs(revision domain.WorkflowRevision, inputs []RunInput) ([]domain.NodeResourceBinding, error) {
	result := make([]domain.NodeResourceBinding, 0, len(inputs))
	declared := make(map[string]domain.WorkflowPort, len(revision.Inputs))
	for _, workflowPort := range revision.Inputs {
		declared[workflowPort.Name] = workflowPort
	}
	supplied := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		if strings.TrimSpace(input.Port) == "" || strings.TrimSpace(input.ResourceID) == "" || input.ResourceType == "" {
			return nil, fmt.Errorf("运行输入必须包含 port、resource_type 和 resource_id")
		}
		workflowPort, exists := declared[input.Port]
		if !exists || workflowPort.ResourceType != input.ResourceType {
			return nil, fmt.Errorf("运行输入 %s 不存在或资源类型不匹配", input.Port)
		}
		if supplied[input.Port] {
			return nil, fmt.Errorf("运行输入 %s 重复", input.Port)
		}
		supplied[input.Port] = true
		for _, connection := range revision.Connections {
			if connection.From.Kind != domain.EndpointWorkflowInput || connection.From.Port != input.Port || connection.To.Kind != domain.EndpointNodeInput {
				continue
			}
			result = append(result, domain.NodeResourceBinding{ID: uuid.NewString(), NodeID: connection.To.NodeID, Port: connection.To.Port,
				Direction: domain.BindingInput, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
				Boundary: append(json.RawMessage(nil), input.Boundary...), Provenance: json.RawMessage(`{"producer":"workflow_input"}`)})
		}
	}
	for _, workflowPort := range revision.Inputs {
		if workflowPort.Required && !supplied[workflowPort.Name] {
			return nil, fmt.Errorf("缺少必填流程输入 %s", workflowPort.Name)
		}
	}
	return result, nil
}

func findWorkflowInput(ports []domain.WorkflowPort, name string) (domain.WorkflowPort, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return domain.WorkflowPort{}, false
}

func findNodeRun(nodes []domain.NodeRun, nodeID string) (domain.NodeRun, bool) {
	for _, node := range nodes {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return domain.NodeRun{}, false
}

func validateNodeOutputs(node domain.NodeRun, outputs []domain.NodeResourceBinding) error {
	seen := map[string]bool{}
	for _, output := range outputs {
		if output.Direction != "" && output.Direction != domain.BindingOutput {
			return fmt.Errorf("节点产物方向非法")
		}
		if output.Port == "" || output.ResourceID == "" {
			return fmt.Errorf("节点产物必须包含 port 和 resource_id")
		}
		definition, ok := findPort(node.Node.Capability.Outputs, output.Port)
		if !ok {
			return fmt.Errorf("节点产物端口 %s 不存在", output.Port)
		}
		if definition.ResourceType != output.ResourceType {
			return fmt.Errorf("节点产物 %s 类型不匹配", output.Port)
		}
		if seen[output.Port] {
			return fmt.Errorf("节点产物端口 %s 重复", output.Port)
		}
		seen[output.Port] = true
	}
	for _, definition := range node.Node.Capability.Outputs {
		if definition.Required && !seen[definition.Name] {
			return fmt.Errorf("节点必填产物 %s 缺失", definition.Name)
		}
	}
	return nil
}

func validateNodeInputs(node domain.NodeRun, inputs []domain.NodeResourceBinding) error {
	counts := make(map[string]int, len(inputs))
	for _, input := range inputs {
		if input.Direction != domain.BindingInput {
			return fmt.Errorf("节点输入方向非法")
		}
		definition, ok := findPort(node.Node.Capability.Inputs, input.Port)
		if !ok || definition.ResourceType != input.ResourceType || strings.TrimSpace(input.ResourceID) == "" {
			return fmt.Errorf("节点输入 %s 不存在、类型不匹配或资源为空", input.Port)
		}
		counts[input.Port]++
		if !definition.Multiple && counts[input.Port] > 1 {
			return fmt.Errorf("节点单值输入 %s 重复", input.Port)
		}
	}
	for _, definition := range node.Node.Capability.Inputs {
		if definition.Required && counts[definition.Name] == 0 {
			return fmt.Errorf("节点必填输入 %s 缺失", definition.Name)
		}
	}
	return nil
}

func findPort(ports []domain.PortDefinition, name string) (domain.PortDefinition, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return domain.PortDefinition{}, false
}

func hasJSONContent(raw json.RawMessage) bool {
	var value any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func IsModelFailure(err error) bool {
	var modelErr *baseagent.ModelError
	return errors.As(err, &modelErr)
}

func retryableNodeError(err error) bool {
	var retryable interface{ Retryable() bool }
	return errors.As(err, &retryable) && retryable.Retryable()
}

func retryAt(now time.Time, attempt int) time.Time {
	backoff := attempt
	if backoff < 1 {
		backoff = 1
	}
	if backoff > 6 {
		backoff = 6
	}
	return now.Add(time.Duration(1<<(backoff-1)) * time.Second)
}
