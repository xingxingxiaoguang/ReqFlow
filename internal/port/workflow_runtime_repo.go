package port

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	domain "reqflow/internal/domain/workflow"
)

var (
	ErrWorkflowRunNotFound  = errors.New("workflow run not found")
	ErrNodeRunNotFound      = errors.New("workflow node run not found")
	ErrRunLeaseLost         = errors.New("workflow node lease lost")
	ErrRunInvalidTransition = errors.New("invalid workflow run transition")
	ErrNoRunnableNode       = errors.New("no runnable workflow node")
)

type WorkflowRunRepo interface {
	CreateWorkflowRun(ctx context.Context, run domain.WorkflowRun, nodes []domain.NodeRun) error
	GetWorkflowRun(ctx context.Context, id string) (*domain.WorkflowRunSnapshot, error)
	ListWorkflowRuns(ctx context.Context, workspaceID string, limit int) ([]domain.WorkflowRunSnapshot, error)
	StartWorkflowRun(ctx context.Context, id string) error
	RequestWorkflowRunPause(ctx context.Context, id string) error
	ResumeWorkflowRun(ctx context.Context, id string) error
	ClaimWorkflowNode(ctx context.Context, owner string, leaseUntil time.Time) (*domain.NodeRun, error)
	GetNodeInputs(ctx context.Context, nodeRunID string) ([]domain.NodeResourceBinding, error)
	RenewWorkflowNodeLease(ctx context.Context, nodeRunID string, attempt int, owner string, leaseUntil time.Time) error
	SaveWorkflowNodeCheckpoint(ctx context.Context, nodeRunID string, attempt int, owner string, checkpoint json.RawMessage) error
	SaveWorkflowNodeProgress(ctx context.Context, nodeRunID string, attempt int, owner string, progress json.RawMessage) error
	CompleteWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner string, outputs []domain.NodeResourceBinding) error
	AwaitWorkflowNodeManual(ctx context.Context, nodeRunID string, attempt int, owner, code, message string) error
	CompleteWorkflowNodeManual(ctx context.Context, nodeRunID string, attempt int, actor string, outputs []domain.NodeResourceBinding) error
	FailWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner, code, message string) error
	RetryWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner, code, message string, retryAt time.Time) error
	RequeueFailedWorkflowNode(ctx context.Context, nodeRunID string) error
	PauseWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner string) error
	RecoverWorkflowNodeLeases(ctx context.Context) (int64, error)
}
