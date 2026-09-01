package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type runtimeMemoryRepo struct {
	snapshot *domain.WorkflowRunSnapshot
	owner    string
	lease    time.Time
	renewErr error
}

func (r *runtimeMemoryRepo) CreateWorkflowRun(_ context.Context, run domain.WorkflowRun, nodes []domain.NodeRun) error {
	bindings := append([]domain.NodeResourceBinding(nil), run.Inputs...)
	for index := range bindings {
		for _, node := range nodes {
			if node.NodeID == bindings[index].NodeID {
				bindings[index].NodeRunID = node.ID
			}
		}
	}
	r.snapshot = &domain.WorkflowRunSnapshot{Run: run, Nodes: nodes, Bindings: bindings}
	return nil
}
func (r *runtimeMemoryRepo) GetWorkflowRun(_ context.Context, _ string) (*domain.WorkflowRunSnapshot, error) {
	if r.snapshot == nil {
		return nil, port.ErrWorkflowRunNotFound
	}
	return r.snapshot, nil
}
func (r *runtimeMemoryRepo) ListWorkflowRuns(context.Context, string, int) ([]domain.WorkflowRunSnapshot, error) {
	return []domain.WorkflowRunSnapshot{*r.snapshot}, nil
}
func (r *runtimeMemoryRepo) StartWorkflowRun(_ context.Context, _ string) error {
	if err := r.snapshot.Run.Start(time.Now()); err != nil {
		return err
	}
	r.snapshot.Nodes[0].Status = domain.NodeQueued
	return nil
}
func (r *runtimeMemoryRepo) RequestWorkflowRunPause(context.Context, string) error {
	return r.snapshot.Run.RequestPause(time.Now())
}
func (r *runtimeMemoryRepo) ResumeWorkflowRun(context.Context, string) error {
	return r.snapshot.Run.Resume(time.Now())
}
func (r *runtimeMemoryRepo) ClaimWorkflowNode(_ context.Context, owner string, leaseUntil time.Time) (*domain.NodeRun, error) {
	for index := range r.snapshot.Nodes {
		node := &r.snapshot.Nodes[index]
		if node.Status != domain.NodeQueued && !(node.Status == domain.NodeRunning && !node.LeaseUntil.After(time.Now())) {
			continue
		}
		node.Status, node.Attempt, node.LeaseOwner, node.LeaseUntil = domain.NodeRunning, node.Attempt+1, owner, leaseUntil
		r.owner, r.lease = owner, leaseUntil
		return node, nil
	}
	return nil, port.ErrNoRunnableNode
}
func (r *runtimeMemoryRepo) GetNodeInputs(_ context.Context, nodeRunID string) ([]domain.NodeResourceBinding, error) {
	var result []domain.NodeResourceBinding
	for _, binding := range r.snapshot.Bindings {
		if binding.NodeRunID == nodeRunID && binding.Direction == domain.BindingInput {
			result = append(result, binding)
		}
	}
	return result, nil
}
func (r *runtimeMemoryRepo) RenewWorkflowNodeLease(context.Context, string, int, string, time.Time) error {
	return r.renewErr
}
func (r *runtimeMemoryRepo) SaveWorkflowNodeCheckpoint(_ context.Context, nodeRunID string, _ int, owner string, checkpoint json.RawMessage) error {
	if r.owner != owner {
		return port.ErrRunLeaseLost
	}
	for index := range r.snapshot.Nodes {
		if r.snapshot.Nodes[index].ID == nodeRunID {
			r.snapshot.Nodes[index].Checkpoint = checkpoint
			return nil
		}
	}
	return port.ErrNodeRunNotFound
}
func (r *runtimeMemoryRepo) SaveWorkflowNodeProgress(context.Context, string, int, string, json.RawMessage) error {
	return nil
}
func (r *runtimeMemoryRepo) CompleteWorkflowNode(_ context.Context, nodeRunID string, _ int, owner string, outputs []domain.NodeResourceBinding) error {
	if r.owner != owner || !r.lease.After(time.Now()) {
		return port.ErrRunLeaseLost
	}
	for index := range r.snapshot.Nodes {
		if r.snapshot.Nodes[index].ID != nodeRunID {
			continue
		}
		node := &r.snapshot.Nodes[index]
		node.Status, node.LeaseOwner = domain.NodeSucceeded, ""
		r.snapshot.Bindings = append(r.snapshot.Bindings, outputs...)
		if index+1 < len(r.snapshot.Nodes) {
			next := r.snapshot.Nodes[index+1]
			for _, output := range outputs {
				for _, connection := range r.snapshot.Run.Revision.Connections {
					if connection.From.NodeID == node.NodeID && connection.From.Port == output.Port && connection.To.NodeID == next.NodeID {
						r.snapshot.Bindings = append(r.snapshot.Bindings, domain.NodeResourceBinding{NodeRunID: next.ID,
							NodeID: next.NodeID, Port: connection.To.Port, Direction: domain.BindingInput,
							ResourceType: output.ResourceType, ResourceID: output.ResourceID})
					}
				}
			}
			r.snapshot.Nodes[index+1].Status, r.snapshot.Run.CurrentNodeID = domain.NodeQueued, r.snapshot.Nodes[index+1].NodeID
		} else {
			r.snapshot.Run.Status, r.snapshot.Run.CurrentNodeID = domain.RunSucceeded, ""
		}
		return nil
	}
	return port.ErrNodeRunNotFound
}
func (r *runtimeMemoryRepo) AwaitWorkflowNodeManual(_ context.Context, nodeRunID string, _ int, owner, code, message string) error {
	if r.owner != owner {
		return port.ErrRunLeaseLost
	}
	for index := range r.snapshot.Nodes {
		if r.snapshot.Nodes[index].ID == nodeRunID {
			r.snapshot.Nodes[index].Status = domain.NodeAwaitingManualCompletion
			r.snapshot.Nodes[index].ErrorCode, r.snapshot.Nodes[index].ErrorMessage = code, message
			r.snapshot.Run.Status = domain.RunAwaitingManualCompletion
			return nil
		}
	}
	return port.ErrNodeRunNotFound
}
func (r *runtimeMemoryRepo) CompleteWorkflowNodeManual(_ context.Context, nodeRunID string, _ int, _ string, outputs []domain.NodeResourceBinding) error {
	for index := range r.snapshot.Nodes {
		if r.snapshot.Nodes[index].ID != nodeRunID {
			continue
		}
		if r.snapshot.Nodes[index].Status != domain.NodeAwaitingManualCompletion {
			return port.ErrRunInvalidTransition
		}
		r.snapshot.Nodes[index].Status = domain.NodeSucceeded
		r.snapshot.Bindings = append(r.snapshot.Bindings, outputs...)
		if index+1 < len(r.snapshot.Nodes) {
			r.snapshot.Nodes[index+1].Status, r.snapshot.Run.Status = domain.NodeQueued, domain.RunRunning
		} else {
			r.snapshot.Run.Status = domain.RunSucceeded
		}
		return nil
	}
	return port.ErrNodeRunNotFound
}
func (r *runtimeMemoryRepo) FailWorkflowNode(context.Context, string, int, string, string, string) error {
	return errors.New("unexpected fail")
}
func (r *runtimeMemoryRepo) RetryWorkflowNode(context.Context, string, int, string, string, string, time.Time) error {
	return errors.New("unexpected retry")
}
func (r *runtimeMemoryRepo) RequeueFailedWorkflowNode(context.Context, string) error {
	return errors.New("unexpected requeue")
}
func (r *runtimeMemoryRepo) PauseWorkflowNode(context.Context, string, int, string) error { return nil }
func (r *runtimeMemoryRepo) RecoverWorkflowNodeLeases(context.Context) (int64, error)     { return 0, nil }

type runtimeRevisionReader struct{ revision domain.WorkflowRevision }

func (r runtimeRevisionReader) GetRevision(context.Context, string) (*domain.WorkflowRevision, error) {
	return &r.revision, nil
}

type runtimeExecutor struct {
	ref     domain.CapabilityRef
	outputs []domain.NodeResourceBinding
	err     error
}

func (e runtimeExecutor) Capability() domain.CapabilityRef { return e.ref }
func (e runtimeExecutor) Execute(context.Context, NodeExecution) (NodeExecutionResult, error) {
	return NodeExecutionResult{Outputs: e.outputs}, e.err
}
func (e runtimeExecutor) Resume(ctx context.Context, execution NodeExecution) (NodeExecutionResult, error) {
	return e.Execute(ctx, execution)
}

type blockingRuntimeExecutor struct{ ref domain.CapabilityRef }

func (e blockingRuntimeExecutor) Capability() domain.CapabilityRef { return e.ref }
func (e blockingRuntimeExecutor) Execute(ctx context.Context, _ NodeExecution) (NodeExecutionResult, error) {
	<-ctx.Done()
	return NodeExecutionResult{}, ctx.Err()
}
func (e blockingRuntimeExecutor) Resume(ctx context.Context, execution NodeExecution) (NodeExecutionResult, error) {
	return e.Execute(ctx, execution)
}

func TestRuntimeAdvancesLinearNodesAndValidatesOutputs(t *testing.T) {
	first := testCapability("runtime.first", "raw", "document")
	second := testCapability("runtime.second", "document", "artifact")
	_, err := domain.NewStaticCatalog(first, second)
	if err != nil {
		t.Fatal(err)
	}
	revision := domain.WorkflowRevision{ID: "revision-1", WorkflowID: "workflow-1", WorkspaceID: "default",
		Inputs: []domain.WorkflowPort{{Name: "source", ResourceType: "raw", Required: true}}, Nodes: []domain.ResolvedNode{
			{ID: "first", Name: "第一步", Capability: first, Config: json.RawMessage(`{}`)},
			{ID: "second", Name: "第二步", Capability: second, Config: json.RawMessage(`{}`)},
		}, Connections: []domain.Connection{
			{From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: "source"}, To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "first", Port: "in"}},
			{From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: "first", Port: "out"}, To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "second", Port: "in"}},
		}}
	registry, err := NewNodeExecutorRegistry(runtimeExecutor{ref: first.Ref, outputs: []domain.NodeResourceBinding{{Port: "out", ResourceType: "document", ResourceID: "doc-1"}}}, runtimeExecutor{ref: second.Ref, outputs: []domain.NodeResourceBinding{{Port: "out", ResourceType: "artifact", ResourceID: "artifact-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeMemoryRepo{}
	service, err := NewRuntimeService(repo, runtimeRevisionReader{revision: revision}, registry, time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Create(context.Background(), CreateRunRequest{RevisionID: revision.ID, Inputs: []RunInput{{Port: "source", ResourceType: "raw", ResourceID: "raw-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background(), run.Run.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.RunOnce(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if repo.snapshot.Nodes[0].Status != domain.NodeSucceeded || repo.snapshot.Nodes[1].Status != domain.NodeQueued {
		t.Fatalf("第一步后状态: %+v", repo.snapshot.Nodes)
	}
	if err := service.RunOnce(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if repo.snapshot.Run.Status != domain.RunSucceeded || len(repo.snapshot.Bindings) != 4 {
		t.Fatalf("运行未完成: run=%+v bindings=%+v", repo.snapshot.Run, repo.snapshot.Bindings)
	}
}

func TestRuntimeModelFailureTransitionsToManualCompletion(t *testing.T) {
	definition := testCapability("runtime.manual", "raw", "artifact")
	definition.ManualCompletion = true
	catalog, err := domain.NewStaticCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	_ = catalog
	revision := domain.WorkflowRevision{ID: "revision-2", WorkflowID: "workflow-2", WorkspaceID: "default",
		Inputs: []domain.WorkflowPort{{Name: "source", ResourceType: "raw", Required: true}},
		Nodes:  []domain.ResolvedNode{{ID: "manual", Name: "模型步骤", Capability: definition, Config: json.RawMessage(`{}`)}},
		Connections: []domain.Connection{{From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: "source"},
			To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "manual", Port: "in"}}}}
	registry, _ := NewNodeExecutorRegistry(runtimeExecutor{ref: definition.Ref, err: errors.New("LLM HTTP 503: unavailable")})
	repo := &runtimeMemoryRepo{}
	service, _ := NewRuntimeService(repo, runtimeRevisionReader{revision: revision}, registry, time.Second, 1)
	run, err := service.Create(context.Background(), CreateRunRequest{RevisionID: revision.ID, Inputs: []RunInput{{Port: "source", ResourceType: "raw", ResourceID: "raw-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background(), run.Run.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.RunOnce(context.Background(), "worker-1"); err != nil {
		t.Fatal(err)
	}
	if repo.snapshot.Run.Status != domain.RunAwaitingManualCompletion || repo.snapshot.Nodes[0].Status != domain.NodeAwaitingManualCompletion {
		t.Fatalf("未进入人工态: %+v", repo.snapshot)
	}
	if err := service.CompleteManual(context.Background(), run.Run.ID, "manual", "user-1", []domain.NodeResourceBinding{{Port: "out", ResourceType: domain.ResourceArtifact, ResourceID: "human-artifact"}}); err != nil {
		t.Fatal(err)
	}
	if repo.snapshot.Run.Status != domain.RunSucceeded || len(repo.snapshot.Bindings) != 2 || repo.snapshot.Bindings[1].Provenance == nil {
		t.Fatalf("人工完成未生效: %+v", repo.snapshot)
	}
}

func TestRuntimeRejectsMissingRequiredInput(t *testing.T) {
	definition := testCapability("runtime.input", "raw", "artifact")
	revision := domain.WorkflowRevision{ID: "revision-input", WorkflowID: "workflow-input", WorkspaceID: "default",
		Inputs: []domain.WorkflowPort{{Name: "source", ResourceType: "raw", Required: true}},
		Nodes:  []domain.ResolvedNode{{ID: "input", Name: "输入", Capability: definition, Config: json.RawMessage(`{}`)}}}
	registry, err := NewNodeExecutorRegistry(runtimeExecutor{ref: definition.Ref})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewRuntimeService(&runtimeMemoryRepo{}, runtimeRevisionReader{revision: revision}, registry, time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), CreateRunRequest{RevisionID: revision.ID}); err == nil {
		t.Fatal("缺少必填流程输入时不应创建运行")
	}
}

func TestRuntimeCancelsExecutionWhenLeaseIsLost(t *testing.T) {
	definition := testCapability("runtime.lease", "raw", "artifact")
	revision := domain.WorkflowRevision{ID: "revision-lease", WorkflowID: "workflow-lease", WorkspaceID: "default",
		Inputs: []domain.WorkflowPort{{Name: "source", ResourceType: "raw", Required: true}},
		Nodes:  []domain.ResolvedNode{{ID: "lease", Name: "租约", Capability: definition, Config: json.RawMessage(`{}`)}},
		Connections: []domain.Connection{{From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: "source"},
			To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "lease", Port: "in"}}}}
	registry, err := NewNodeExecutorRegistry(blockingRuntimeExecutor{ref: definition.Ref})
	if err != nil {
		t.Fatal(err)
	}
	repo := &runtimeMemoryRepo{renewErr: port.ErrRunLeaseLost}
	service, err := NewRuntimeService(repo, runtimeRevisionReader{revision: revision}, registry, 20*time.Millisecond, 0)
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Create(context.Background(), CreateRunRequest{RevisionID: revision.ID, Inputs: []RunInput{{Port: "source", ResourceType: "raw", ResourceID: "raw-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(context.Background(), run.Run.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.RunOnce(context.Background(), "worker-1"); !errors.Is(err, port.ErrRunLeaseLost) {
		t.Fatalf("租约丢失后 RunOnce error=%v, want ErrRunLeaseLost", err)
	}
}
