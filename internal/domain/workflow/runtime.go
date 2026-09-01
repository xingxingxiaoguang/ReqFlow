package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type WorkflowRunStatus string

const (
	RunQueued                   WorkflowRunStatus = "queued"
	RunRunning                  WorkflowRunStatus = "running"
	RunPausing                  WorkflowRunStatus = "pausing"
	RunPaused                   WorkflowRunStatus = "paused"
	RunAwaitingManualCompletion WorkflowRunStatus = "awaiting_manual_completion"
	RunFailed                   WorkflowRunStatus = "failed"
	RunSucceeded                WorkflowRunStatus = "succeeded"
)

type NodeRunStatus string

const (
	NodePending                  NodeRunStatus = "pending"
	NodeQueued                   NodeRunStatus = "queued"
	NodeRunning                  NodeRunStatus = "running"
	NodeRetryWait                NodeRunStatus = "retry_wait"
	NodeAwaitingManualCompletion NodeRunStatus = "awaiting_manual_completion"
	NodeValidating               NodeRunStatus = "validating"
	NodeFailed                   NodeRunStatus = "failed"
	NodeSucceeded                NodeRunStatus = "succeeded"
)

type BindingDirection string

const (
	BindingInput  BindingDirection = "input"
	BindingOutput BindingDirection = "output"
)

type NodeResourceBinding struct {
	ID           string           `json:"id"`
	NodeRunID    string           `json:"node_run_id"`
	NodeID       string           `json:"node_id"`
	Port         string           `json:"port"`
	Direction    BindingDirection `json:"direction"`
	ResourceType ResourceType     `json:"resource_type"`
	ResourceID   string           `json:"resource_id"`
	Boundary     json.RawMessage  `json:"boundary,omitempty"`
	Provenance   json.RawMessage  `json:"provenance,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
}

type WorkflowRun struct {
	ID            string                `json:"id"`
	WorkflowID    string                `json:"workflow_id"`
	RevisionID    string                `json:"revision_id"`
	WorkspaceID   string                `json:"workspace_id"`
	Revision      WorkflowRevision      `json:"revision"`
	Inputs        []NodeResourceBinding `json:"inputs"`
	Status        WorkflowRunStatus     `json:"status"`
	CurrentNodeID string                `json:"current_node_id,omitempty"`
	ErrorCode     string                `json:"error_code,omitempty"`
	ErrorMessage  string                `json:"error_message,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	StartedAt     time.Time             `json:"started_at,omitempty"`
	FinishedAt    time.Time             `json:"finished_at,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

type NodeRun struct {
	ID           string          `json:"id"`
	RunID        string          `json:"run_id"`
	NodeID       string          `json:"node_id"`
	Ordinal      int             `json:"ordinal"`
	Node         ResolvedNode    `json:"node"`
	Status       NodeRunStatus   `json:"status"`
	Attempt      int             `json:"attempt"`
	Checkpoint   json.RawMessage `json:"checkpoint,omitempty"`
	Progress     json.RawMessage `json:"progress,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	LeaseOwner   string          `json:"lease_owner,omitempty"`
	LeaseUntil   time.Time       `json:"lease_until,omitempty"`
	RetryAt      time.Time       `json:"retry_at,omitempty"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	FinishedAt   time.Time       `json:"finished_at,omitempty"`
}

type WorkflowRunSnapshot struct {
	Run      WorkflowRun           `json:"run"`
	Nodes    []NodeRun             `json:"nodes"`
	Bindings []NodeResourceBinding `json:"bindings"`
}

func NewWorkflowRun(id string, revision WorkflowRevision, inputs []NodeResourceBinding, now time.Time) (*WorkflowRun, []NodeRun, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(revision.ID) == "" || now.IsZero() {
		return nil, nil, fmt.Errorf("WorkflowRun id、Revision 和时间必须有效")
	}
	if len(revision.Nodes) == 0 {
		return nil, nil, fmt.Errorf("WorkflowRun 至少需要一个节点")
	}
	run := &WorkflowRun{ID: id, WorkflowID: revision.WorkflowID, RevisionID: revision.ID, WorkspaceID: revision.WorkspaceID,
		Revision: revision, Inputs: append([]NodeResourceBinding(nil), inputs...), Status: RunQueued, CreatedAt: now, UpdatedAt: now}
	nodes := make([]NodeRun, len(revision.Nodes))
	for index, node := range revision.Nodes {
		nodes[index] = NodeRun{ID: fmt.Sprintf("%s-node-%d", id, index+1), RunID: id, NodeID: node.ID,
			Ordinal: index + 1, Node: node, Status: NodePending, Checkpoint: json.RawMessage(`{}`), Progress: json.RawMessage(`{}`)}
	}
	return run, nodes, nil
}

func (run *WorkflowRun) Start(now time.Time) error {
	if run == nil || now.IsZero() || run.Status != RunQueued {
		return fmt.Errorf("WorkflowRun 只能从 queued 启动")
	}
	run.Status, run.StartedAt, run.UpdatedAt = RunRunning, now, now
	return nil
}

func (run *WorkflowRun) RequestPause(now time.Time) error {
	if run == nil || run.Status != RunRunning {
		return fmt.Errorf("只有 running WorkflowRun 可以请求暂停")
	}
	run.Status, run.UpdatedAt = RunPausing, now
	return nil
}

func (run *WorkflowRun) Resume(now time.Time) error {
	if run == nil || now.IsZero() || run.Status != RunPaused {
		return fmt.Errorf("WorkflowRun 只能从 paused 恢复")
	}
	run.Status, run.UpdatedAt = RunRunning, now
	return nil
}

func (run *WorkflowRun) AwaitManual(nodeID string, now time.Time) error {
	if run == nil || now.IsZero() || run.Status != RunRunning || strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("WorkflowRun 不能进入人工完成状态")
	}
	run.CurrentNodeID, run.Status, run.UpdatedAt = nodeID, RunAwaitingManualCompletion, now
	return nil
}
