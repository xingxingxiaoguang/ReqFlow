package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type workflowRunRow struct {
	ID            string     `gorm:"column:id;primaryKey"`
	WorkflowID    string     `gorm:"column:workflow_id"`
	RevisionID    string     `gorm:"column:revision_id"`
	WorkspaceID   string     `gorm:"column:workspace_id"`
	Revision      string     `gorm:"column:revision;type:jsonb"`
	Inputs        string     `gorm:"column:inputs;type:jsonb"`
	Status        string     `gorm:"column:status"`
	CurrentNodeID string     `gorm:"column:current_node_id"`
	ErrorCode     string     `gorm:"column:error_code"`
	ErrorMessage  string     `gorm:"column:error_message"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	StartedAt     *time.Time `gorm:"column:started_at"`
	FinishedAt    *time.Time `gorm:"column:finished_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
}

func (workflowRunRow) TableName() string { return "workflow_runs" }

type workflowNodeRunRow struct {
	ID           string     `gorm:"column:id;primaryKey"`
	RunID        string     `gorm:"column:run_id"`
	NodeID       string     `gorm:"column:node_id"`
	Ordinal      int        `gorm:"column:ordinal"`
	Node         string     `gorm:"column:node;type:jsonb"`
	Status       string     `gorm:"column:status"`
	Attempt      int        `gorm:"column:attempt"`
	Checkpoint   string     `gorm:"column:checkpoint;type:jsonb"`
	Progress     string     `gorm:"column:progress;type:jsonb"`
	ErrorCode    string     `gorm:"column:error_code"`
	ErrorMessage string     `gorm:"column:error_message"`
	LeaseOwner   string     `gorm:"column:lease_owner"`
	LeaseUntil   *time.Time `gorm:"column:lease_until"`
	RetryAt      *time.Time `gorm:"column:retry_at"`
	StartedAt    *time.Time `gorm:"column:started_at"`
	FinishedAt   *time.Time `gorm:"column:finished_at"`
}

func (workflowNodeRunRow) TableName() string { return "workflow_node_runs" }

type nodeResourceBindingRow struct {
	ID           string    `gorm:"column:id;primaryKey"`
	RunID        string    `gorm:"column:run_id"`
	NodeRunID    string    `gorm:"column:node_run_id"`
	NodeID       string    `gorm:"column:node_id"`
	Port         string    `gorm:"column:port"`
	Direction    string    `gorm:"column:direction"`
	ResourceType string    `gorm:"column:resource_type"`
	ResourceID   string    `gorm:"column:resource_id"`
	Boundary     string    `gorm:"column:boundary;type:jsonb"`
	Provenance   string    `gorm:"column:provenance;type:jsonb"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (nodeResourceBindingRow) TableName() string { return "node_resource_bindings" }

func (r *WorkflowRepo) CreateWorkflowRun(ctx context.Context, run domain.WorkflowRun, nodes []domain.NodeRun) error {
	revision, err := json.Marshal(run.Revision)
	if err != nil {
		return err
	}
	inputs, err := json.Marshal(run.Inputs)
	if err != nil {
		return err
	}
	now := run.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&workflowRunRow{ID: run.ID, WorkflowID: run.WorkflowID, RevisionID: run.RevisionID,
			WorkspaceID: run.WorkspaceID, Revision: string(revision), Inputs: string(inputs), Status: string(run.Status),
			CurrentNodeID: run.CurrentNodeID, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
			CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		for _, node := range nodes {
			document, err := json.Marshal(node.Node)
			if err != nil {
				return err
			}
			if err := tx.Create(&workflowNodeRunRow{ID: node.ID, RunID: run.ID, NodeID: node.NodeID, Ordinal: node.Ordinal,
				Node: string(document), Status: string(node.Status), Attempt: node.Attempt, Checkpoint: string(jsonOrEmpty(node.Checkpoint)),
				Progress: string(jsonOrEmpty(node.Progress)), ErrorCode: node.ErrorCode, ErrorMessage: node.ErrorMessage,
				LeaseOwner: node.LeaseOwner, LeaseUntil: timePtr(node.LeaseUntil), RetryAt: timePtr(node.RetryAt),
				StartedAt: timePtr(node.StartedAt), FinishedAt: timePtr(node.FinishedAt)}).Error; err != nil {
				return err
			}
		}
		for _, input := range run.Inputs {
			var node workflowNodeRunRow
			if err := tx.Where("run_id = ? AND node_id = ?", run.ID, input.NodeID).First(&node).Error; err != nil {
				return err
			}
			if err := tx.Create(&nodeResourceBindingRow{ID: nonEmptyUUID(input.ID), RunID: run.ID, NodeRunID: node.ID,
				NodeID: input.NodeID, Port: input.Port, Direction: string(domain.BindingInput), ResourceType: string(input.ResourceType),
				ResourceID: input.ResourceID, Boundary: string(jsonOrEmpty(input.Boundary)), Provenance: string(jsonOrEmpty(input.Provenance)), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *WorkflowRepo) GetWorkflowRun(ctx context.Context, id string) (*domain.WorkflowRunSnapshot, error) {
	var runRow workflowRunRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&runRow).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrWorkflowRunNotFound
	} else if err != nil {
		return nil, err
	}
	run, err := workflowRunFromRow(runRow)
	if err != nil {
		return nil, err
	}
	var nodeRows []workflowNodeRunRow
	if err := r.db.WithContext(ctx).Where("run_id = ?", id).Order("ordinal").Find(&nodeRows).Error; err != nil {
		return nil, err
	}
	nodes := make([]domain.NodeRun, 0, len(nodeRows))
	for _, row := range nodeRows {
		node, err := nodeRunFromRow(row)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	bindings, err := r.bindings(ctx, id, "")
	if err != nil {
		return nil, err
	}
	return &domain.WorkflowRunSnapshot{Run: run, Nodes: nodes, Bindings: bindings}, nil
}

func (r *WorkflowRepo) ListWorkflowRuns(ctx context.Context, workspaceID string, limit int) ([]domain.WorkflowRunSnapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows []workflowRunRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.WorkflowRunSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot, err := r.GetWorkflowRun(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, *snapshot)
	}
	return result, nil
}

func (r *WorkflowRepo) StartWorkflowRun(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run workflowRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowRunNotFound
		} else if err != nil {
			return err
		}
		if run.Status != string(domain.RunQueued) {
			return port.ErrRunInvalidTransition
		}
		var node workflowNodeRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ? AND ordinal = 1", id).First(&node).Error; err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ? AND status = ?", node.ID, domain.NodePending).Updates(map[string]any{"status": domain.NodeQueued}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ? AND status = ?", id, domain.RunQueued).Updates(map[string]any{"status": domain.RunRunning, "current_node_id": node.NodeID, "started_at": now, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) RequestWorkflowRunPause(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run workflowRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowRunNotFound
		} else if err != nil {
			return err
		}
		if run.Status != string(domain.RunRunning) {
			return port.ErrRunInvalidTransition
		}
		var node workflowNodeRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("run_id = ? AND node_id = ?", id, run.CurrentNodeID).First(&node).Error; err != nil {
			return err
		}
		now := time.Now()
		if node.Status == string(domain.NodeRunning) {
			return tx.Model(&workflowRunRow{}).Where("id = ?", id).Updates(map[string]any{"status": domain.RunPausing, "updated_at": now}).Error
		}
		if node.Status != string(domain.NodeQueued) && node.Status != string(domain.NodePending) && node.Status != string(domain.NodeRetryWait) {
			return port.ErrRunInvalidTransition
		}
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodePending, "retry_at": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", id).Updates(map[string]any{"status": domain.RunPaused, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) ResumeWorkflowRun(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run workflowRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&run).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrWorkflowRunNotFound
		} else if err != nil {
			return err
		}
		if run.Status != string(domain.RunPaused) {
			return port.ErrRunInvalidTransition
		}
		var node workflowNodeRunRow
		if err := tx.Where("run_id = ? AND status IN (?, ?) ORDER BY ordinal LIMIT 1", id, domain.NodePending, domain.NodeRetryWait).First(&node).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrRunInvalidTransition
		} else if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodeQueued, "retry_at": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ? AND status = ?", id, domain.RunPaused).Updates(map[string]any{"status": domain.RunRunning, "current_node_id": node.NodeID, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) ClaimWorkflowNode(ctx context.Context, owner string, leaseUntil time.Time) (*domain.NodeRun, error) {
	var result domain.NodeRun
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row workflowNodeRunRow
		// retry_wait is intentionally delayed; an expired running lease is recoverable
		// regardless of the previous retry timestamp.
		query := tx.Raw(`SELECT n.* FROM workflow_node_runs n JOIN workflow_runs r ON r.id=n.run_id
				WHERE (n.status = ? OR (n.status = ? AND n.retry_at IS NOT NULL AND n.retry_at <= ?) OR (n.status = ? AND n.lease_until IS NOT NULL AND n.lease_until < ?))
				AND r.status IN (?, ?) ORDER BY n.ordinal LIMIT 1 FOR UPDATE SKIP LOCKED`, domain.NodeQueued, domain.NodeRetryWait, time.Now(), domain.NodeRunning, time.Now(), domain.RunRunning, domain.RunPausing)
		if err := query.Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == "" {
			return port.ErrNoRunnableNode
		}
		now := time.Now()
		updates := map[string]any{"status": domain.NodeRunning, "attempt": row.Attempt + 1, "lease_owner": owner, "lease_until": leaseUntil, "retry_at": nil, "started_at": now, "error_code": "", "error_message": ""}
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return err
		}
		row.Status, row.Attempt, row.LeaseOwner, row.LeaseUntil, row.RetryAt, row.StartedAt = string(domain.NodeRunning), row.Attempt+1, owner, &leaseUntil, nil, &now
		var err error
		result, err = nodeRunFromRow(row)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *WorkflowRepo) GetNodeInputs(ctx context.Context, nodeRunID string) ([]domain.NodeResourceBinding, error) {
	return r.bindings(ctx, "", nodeRunID)
}

func (r *WorkflowRepo) RenewWorkflowNodeLease(ctx context.Context, nodeRunID string, attempt int, owner string, leaseUntil time.Time) error {
	result := r.db.WithContext(ctx).Model(&workflowNodeRunRow{}).Where("id = ? AND attempt = ? AND lease_owner = ? AND status = ? AND lease_until > ?", nodeRunID, attempt, owner, domain.NodeRunning, time.Now()).Updates(map[string]any{"lease_until": leaseUntil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return port.ErrRunLeaseLost
	}
	return nil
}

func (r *WorkflowRepo) SaveWorkflowNodeCheckpoint(ctx context.Context, nodeRunID string, attempt int, owner string, checkpoint json.RawMessage) error {
	return r.saveOwnedNodeField(ctx, nodeRunID, attempt, owner, "checkpoint", string(jsonOrEmpty(checkpoint)))
}
func (r *WorkflowRepo) SaveWorkflowNodeProgress(ctx context.Context, nodeRunID string, attempt int, owner string, progress json.RawMessage) error {
	return r.saveOwnedNodeField(ctx, nodeRunID, attempt, owner, "progress", string(jsonOrEmpty(progress)))
}

func (r *WorkflowRepo) CompleteWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner string, outputs []domain.NodeResourceBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, run, err := r.lockNodeAndRun(tx, nodeRunID, attempt, owner, domain.NodeRunning)
		if err != nil {
			return err
		}
		if err := insertNodeBindings(tx, run.ID, node, outputs); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodeSucceeded, "lease_owner": "", "lease_until": nil, "finished_at": now}).Error; err != nil {
			return err
		}
		return advanceRun(tx, run, node, outputs, now)
	})
}

func (r *WorkflowRepo) AwaitWorkflowNodeManual(ctx context.Context, nodeRunID string, attempt int, owner, code, message string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, run, err := r.lockNodeAndRun(tx, nodeRunID, attempt, owner, domain.NodeRunning)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodeAwaitingManualCompletion, "error_code": code, "error_message": message, "lease_owner": "", "lease_until": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunAwaitingManualCompletion, "current_node_id": node.NodeID, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) CompleteWorkflowNodeManual(ctx context.Context, nodeRunID string, attempt int, actor string, outputs []domain.NodeResourceBinding) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node workflowNodeRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", nodeRunID).First(&node).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrNodeRunNotFound
		} else if err != nil {
			return err
		}
		if node.Status != string(domain.NodeAwaitingManualCompletion) || node.Attempt != attempt {
			return port.ErrRunInvalidTransition
		}
		var run workflowRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", node.RunID).First(&run).Error; err != nil {
			return err
		}
		for index := range outputs {
			outputs[index].Provenance = json.RawMessage(fmt.Sprintf(`{"producer":"human","actor":%q}`, actor))
		}
		if err := insertNodeBindings(tx, run.ID, node, outputs); err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ? AND status = ?", node.ID, domain.NodeAwaitingManualCompletion).Updates(map[string]any{"status": domain.NodeSucceeded, "finished_at": now}).Error; err != nil {
			return err
		}
		return advanceRun(tx, run, node, outputs, now)
	})
}

func (r *WorkflowRepo) FailWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner, code, message string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, run, err := r.lockNodeAndRun(tx, nodeRunID, attempt, owner, domain.NodeRunning)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodeFailed, "error_code": code, "error_message": message, "lease_owner": "", "lease_until": nil, "finished_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunFailed, "error_code": code, "error_message": message, "finished_at": now, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) RetryWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner, code, message string, retryAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, run, err := r.lockNodeAndRun(tx, nodeRunID, attempt, owner, domain.NodeRunning)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ? AND attempt = ?", node.ID, attempt).Updates(map[string]any{
			"status": domain.NodeRetryWait, "error_code": code, "error_message": message, "lease_owner": "", "lease_until": nil, "retry_at": retryAt,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunRunning, "current_node_id": node.NodeID, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) RequeueFailedWorkflowNode(ctx context.Context, nodeRunID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node workflowNodeRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", nodeRunID).First(&node).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return port.ErrNodeRunNotFound
		} else if err != nil {
			return err
		}
		if node.Status != string(domain.NodeFailed) {
			return port.ErrRunInvalidTransition
		}
		var run workflowRunRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", node.RunID).First(&run).Error; err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodeQueued, "retry_at": nil, "error_code": "", "error_message": "", "finished_at": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunRunning, "current_node_id": node.NodeID, "error_code": "", "error_message": "", "finished_at": nil, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) PauseWorkflowNode(ctx context.Context, nodeRunID string, attempt int, owner string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		node, run, err := r.lockNodeAndRun(tx, nodeRunID, attempt, owner, domain.NodeRunning)
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&workflowNodeRunRow{}).Where("id = ?", node.ID).Updates(map[string]any{"status": domain.NodePending, "lease_owner": "", "lease_until": nil}).Error; err != nil {
			return err
		}
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunPaused, "current_node_id": node.NodeID, "updated_at": now}).Error
	})
}

func (r *WorkflowRepo) RecoverWorkflowNodeLeases(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Model(&workflowNodeRunRow{}).Where("status = ? AND lease_until IS NOT NULL AND lease_until < ?", domain.NodeRunning, time.Now()).Updates(map[string]any{"status": domain.NodeQueued, "lease_owner": "", "lease_until": nil})
	return result.RowsAffected, result.Error
}

func (r *WorkflowRepo) lockNodeAndRun(tx *gorm.DB, nodeRunID string, attempt int, owner string, status domain.NodeRunStatus) (workflowNodeRunRow, workflowRunRow, error) {
	var node workflowNodeRunRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", nodeRunID).First(&node).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return node, workflowRunRow{}, port.ErrNodeRunNotFound
	} else if err != nil {
		return node, workflowRunRow{}, err
	}
	if node.Status != string(status) || node.Attempt != attempt || node.LeaseOwner != owner || node.LeaseUntil == nil || !node.LeaseUntil.After(time.Now()) {
		return node, workflowRunRow{}, port.ErrRunLeaseLost
	}
	var run workflowRunRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", node.RunID).First(&run).Error; err != nil {
		return node, run, err
	}
	return node, run, nil
}

func advanceRun(tx *gorm.DB, run workflowRunRow, node workflowNodeRunRow, outputs []domain.NodeResourceBinding, now time.Time) error {
	if err := propagateNodeOutputs(tx, run, node, outputs, now); err != nil {
		return err
	}
	var next workflowNodeRunRow
	if err := tx.Where("run_id = ? AND ordinal = ?", run.ID, node.Ordinal+1).First(&next).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunSucceeded, "current_node_id": "", "finished_at": now, "updated_at": now}).Error
	} else if err != nil {
		return err
	}
	if run.Status == string(domain.RunPausing) {
		return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunPaused, "current_node_id": next.NodeID, "updated_at": now}).Error
	}
	if err := tx.Model(&workflowNodeRunRow{}).Where("id = ? AND status = ?", next.ID, domain.NodePending).Update("status", domain.NodeQueued).Error; err != nil {
		return err
	}
	return tx.Model(&workflowRunRow{}).Where("id = ?", run.ID).Updates(map[string]any{"status": domain.RunRunning, "current_node_id": next.NodeID, "updated_at": now}).Error
}

func propagateNodeOutputs(tx *gorm.DB, run workflowRunRow, node workflowNodeRunRow, outputs []domain.NodeResourceBinding, now time.Time) error {
	var revision domain.WorkflowRevision
	if err := json.Unmarshal([]byte(run.Revision), &revision); err != nil {
		return fmt.Errorf("解析 WorkflowRevision: %w", err)
	}
	var next workflowNodeRunRow
	if err := tx.Where("run_id = ? AND ordinal = ?", run.ID, node.Ordinal+1).First(&next).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	for _, output := range outputs {
		for _, connection := range revision.Connections {
			if connection.From.Kind != domain.EndpointNodeOutput || connection.From.NodeID != node.NodeID || connection.From.Port != output.Port || connection.To.Kind != domain.EndpointNodeInput || connection.To.NodeID != next.NodeID {
				continue
			}
			if err := tx.Create(&nodeResourceBindingRow{ID: uuid.NewString(), RunID: run.ID, NodeRunID: next.ID, NodeID: next.NodeID,
				Port: connection.To.Port, Direction: string(domain.BindingInput), ResourceType: string(output.ResourceType), ResourceID: output.ResourceID,
				Boundary: string(jsonOrEmpty(output.Boundary)), Provenance: string(jsonOrEmpty(output.Provenance)), CreatedAt: now}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func insertNodeBindings(tx *gorm.DB, runID string, node workflowNodeRunRow, outputs []domain.NodeResourceBinding) error {
	now := time.Now()
	for _, output := range outputs {
		if output.ID == "" {
			output.ID = uuid.NewString()
		}
		if err := tx.Create(&nodeResourceBindingRow{ID: output.ID, RunID: runID, NodeRunID: node.ID, NodeID: node.NodeID,
			Port: output.Port, Direction: string(domain.BindingOutput), ResourceType: string(output.ResourceType), ResourceID: output.ResourceID,
			Boundary: string(jsonOrEmpty(output.Boundary)), Provenance: string(jsonOrEmpty(output.Provenance)), CreatedAt: now}).Error; err != nil {
			return err
		}
	}
	return nil
}

func nonEmptyUUID(value string) string {
	if value == "" {
		return uuid.NewString()
	}
	return value
}

func (r *WorkflowRepo) saveOwnedNodeField(ctx context.Context, nodeRunID string, attempt int, owner, field, value string) error {
	if field != "checkpoint" && field != "progress" {
		return fmt.Errorf("invalid node field")
	}
	result := r.db.WithContext(ctx).Model(&workflowNodeRunRow{}).Where("id = ? AND attempt = ? AND lease_owner = ? AND status = ? AND lease_until > ?", nodeRunID, attempt, owner, domain.NodeRunning, time.Now()).Update(field, value)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return port.ErrRunLeaseLost
	}
	return nil
}

func (r *WorkflowRepo) bindings(ctx context.Context, runID, nodeRunID string) ([]domain.NodeResourceBinding, error) {
	var rows []nodeResourceBindingRow
	query := r.db.WithContext(ctx)
	if runID != "" {
		query = query.Where("run_id = ?", runID)
	}
	if nodeRunID != "" {
		query = query.Where("node_run_id = ? AND direction = ?", nodeRunID, domain.BindingInput)
	}
	if err := query.Order("created_at").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]domain.NodeResourceBinding, 0, len(rows))
	for _, row := range rows {
		result = append(result, domain.NodeResourceBinding{ID: row.ID, NodeRunID: row.NodeRunID, NodeID: row.NodeID, Port: row.Port,
			Direction: domain.BindingDirection(row.Direction), ResourceType: domain.ResourceType(row.ResourceType), ResourceID: row.ResourceID,
			Boundary: json.RawMessage(row.Boundary), Provenance: json.RawMessage(row.Provenance), CreatedAt: row.CreatedAt})
	}
	return result, nil
}

func workflowRunFromRow(row workflowRunRow) (domain.WorkflowRun, error) {
	var revision domain.WorkflowRevision
	if err := json.Unmarshal([]byte(row.Revision), &revision); err != nil {
		return domain.WorkflowRun{}, err
	}
	var inputs []domain.NodeResourceBinding
	if err := json.Unmarshal([]byte(row.Inputs), &inputs); err != nil {
		return domain.WorkflowRun{}, err
	}
	run := domain.WorkflowRun{ID: row.ID, WorkflowID: row.WorkflowID, RevisionID: row.RevisionID, WorkspaceID: row.WorkspaceID,
		Revision: revision, Inputs: inputs, Status: domain.WorkflowRunStatus(row.Status), CurrentNodeID: row.CurrentNodeID,
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.StartedAt != nil {
		run.StartedAt = *row.StartedAt
	}
	if row.FinishedAt != nil {
		run.FinishedAt = *row.FinishedAt
	}
	return run, nil
}

func nodeRunFromRow(row workflowNodeRunRow) (domain.NodeRun, error) {
	var node domain.ResolvedNode
	if err := json.Unmarshal([]byte(row.Node), &node); err != nil {
		return domain.NodeRun{}, err
	}
	result := domain.NodeRun{ID: row.ID, RunID: row.RunID, NodeID: row.NodeID, Ordinal: row.Ordinal, Node: node,
		Status: domain.NodeRunStatus(row.Status), Attempt: row.Attempt, Checkpoint: json.RawMessage(row.Checkpoint), Progress: json.RawMessage(row.Progress),
		ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage, LeaseOwner: row.LeaseOwner}
	if row.LeaseUntil != nil {
		result.LeaseUntil = *row.LeaseUntil
	}
	if row.RetryAt != nil {
		result.RetryAt = *row.RetryAt
	}
	if row.StartedAt != nil {
		result.StartedAt = *row.StartedAt
	}
	if row.FinishedAt != nil {
		result.FinishedAt = *row.FinishedAt
	}
	return result, nil
}

var _ port.WorkflowRunRepo = (*WorkflowRepo)(nil)
