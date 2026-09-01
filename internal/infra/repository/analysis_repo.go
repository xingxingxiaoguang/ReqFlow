package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"reqflow/internal/domain/model"
)

func (r *PipelineRepo) BeginAnalysisResult(ctx context.Context, result *model.AnalysisResult,
	producerAttempt int) (*model.AnalysisResult, error) {
	if result == nil || result.ProducerNodeRunID == "" || result.ProducerWorkflowRunID == "" ||
		strings.TrimSpace(result.Instruction) == "" || len(result.OutputContract) == 0 ||
		strings.TrimSpace(result.OutputContractHash) == "" || len(result.OutputSchema) == 0 ||
		strings.TrimSpace(result.OutputSchemaHash) == "" {
		return nil, fmt.Errorf("AnalysisResult 必须绑定 WorkflowRun、NodeRun 和完整输出合同")
	}
	if result.ID == "" {
		result.ID = uuid.NewString()
	}
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now()
	}
	if result.WorkspaceID == "" {
		result.WorkspaceID = "default"
	}
	var stored *model.AnalysisResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, result.ProducerNodeRunID, producerAttempt); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO analysis_results
			(id, workspace_id, instruction, output_contract, output_contract_hash,
			 output_schema, output_schema_hash, producer_workflow_run_id, producer_node_run_id,
			 producer_attempt, status, created_at)
			VALUES (?, ?, ?, ?::jsonb, ?, ?::jsonb, ?, ?, ?, ?, 'running', ?)
			ON CONFLICT (producer_node_run_id) DO NOTHING`, result.ID, result.WorkspaceID,
			result.Instruction, string(result.OutputContract), result.OutputContractHash,
			string(result.OutputSchema), result.OutputSchemaHash,
			result.ProducerWorkflowRunID, result.ProducerNodeRunID,
			producerAttempt, result.CreatedAt).Error; err != nil {
			return err
		}
		var row analysisResultRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("producer_node_run_id = ?", result.ProducerNodeRunID).First(&row).Error; err != nil {
			return err
		}
		if row.Instruction != result.Instruction || row.OutputContractHash != result.OutputContractHash ||
			row.OutputSchemaHash != result.OutputSchemaHash || !equalJSON(row.OutputContract, result.OutputContract) ||
			!equalJSON(row.OutputSchema, result.OutputSchema) ||
			row.ProducerWorkflowRunID != result.ProducerWorkflowRunID || row.WorkspaceID != result.WorkspaceID {
			return fmt.Errorf("NodeRun 已绑定到不同的 AnalysisResult")
		}
		if row.Status != model.AnalysisResultSucceeded {
			if err := tx.Model(&analysisResultRow{}).Where("id = ?", row.ID).Updates(map[string]any{
				"status": model.AnalysisResultRunning, "producer_attempt": producerAttempt,
				"error_message": "", "finished_at": nil,
			}).Error; err != nil {
				return err
			}
			row.Status, row.ProducerAttempt, row.ErrorMessage, row.FinishedAt =
				model.AnalysisResultRunning, producerAttempt, "", nil
		}
		stored = row.toModel()
		return nil
	})
	return stored, err
}

func (r *PipelineRepo) GetAnalysisResult(ctx context.Context, id string) (*model.AnalysisResult, error) {
	var row analysisResultRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

func (r *PipelineRepo) CompleteAnalysisResult(ctx context.Context, result *model.AnalysisResult,
	producerAttempt int) error {
	if result == nil || len(result.Output) == 0 || len(result.AgentContext) == 0 {
		return fmt.Errorf("完成 AnalysisResult 必须提供 output 和 agent_context")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, result.ProducerNodeRunID, producerAttempt); err != nil {
			return err
		}
		now := time.Now()
		updated := tx.Model(&analysisResultRow{}).
			Where("id = ? AND producer_node_run_id = ? AND producer_attempt = ? AND status = ?",
				result.ID, result.ProducerNodeRunID, producerAttempt, model.AnalysisResultRunning).
			Updates(map[string]any{"status": model.AnalysisResultSucceeded,
				"output": string(result.Output), "agent_context": string(result.AgentContext),
				"model": result.Model, "input_tokens": result.InputTokens, "output_tokens": result.OutputTokens,
				"cache_read_tokens": result.CacheReadTokens, "cache_write_tokens": result.CacheWriteTokens,
				"error_message": "", "finished_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("AnalysisResult 完成写入被 fencing 拒绝")
		}
		return nil
	})
}

func (r *PipelineRepo) FailAnalysisResult(ctx context.Context, id, nodeRunID string,
	producerAttempt int, message string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, nodeRunID, producerAttempt); err != nil {
			return err
		}
		updated := tx.Model(&analysisResultRow{}).
			Where("id = ? AND producer_node_run_id = ? AND producer_attempt = ? AND status <> ?",
				id, nodeRunID, producerAttempt, model.AnalysisResultSucceeded).
			Updates(map[string]any{"status": model.AnalysisResultFailed,
				"error_message": message, "finished_at": time.Now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("AnalysisResult 失败写入被 fencing 拒绝")
		}
		return nil
	})
}

func (r *PipelineRepo) CreateArtifactForNode(ctx context.Context, artifact *model.Artifact,
	producerAttempt int) (*model.Artifact, error) {
	if artifact == nil || artifact.ProducerNodeRunID == "" || artifact.ProducerWorkflowRunID == "" || artifact.ContentHash == "" {
		return nil, fmt.Errorf("Artifact 必须绑定 WorkflowRun、NodeRun 和 content_hash")
	}
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	if strings.TrimSpace(artifact.Metadata) == "" {
		artifact.Metadata = "{}"
	}
	var stored *model.Artifact
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertActiveNodeProducer(tx, artifact.ProducerNodeRunID, producerAttempt); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO artifacts
			(id, workspace_id, kind, name, blob_uri, content_hash, producer_workflow_run_id,
			 producer_node_run_id, producer_attempt, metadata, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb, ?)
			ON CONFLICT (producer_node_run_id) WHERE producer_node_run_id IS NOT NULL DO NOTHING`,
			artifact.ID, artifact.WorkspaceID, artifact.Kind, artifact.Name, artifact.BlobURI,
			artifact.ContentHash, artifact.ProducerWorkflowRunID, artifact.ProducerNodeRunID, producerAttempt,
			artifact.Metadata, artifact.CreatedAt).Error; err != nil {
			return err
		}
		var row artifactRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("producer_node_run_id = ?", artifact.ProducerNodeRunID).First(&row).Error; err != nil {
			return err
		}
		if row.ContentHash != artifact.ContentHash || row.Kind != artifact.Kind || row.Name != artifact.Name {
			return fmt.Errorf("NodeRun 已绑定到不同的 Artifact")
		}
		stored = row.toModel()
		return nil
	})
	return stored, err
}

func (r *PipelineRepo) GetArtifact(ctx context.Context, id string) (*model.Artifact, error) {
	var row artifactRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	return row.toModel(), nil
}

func (r *PipelineRepo) ListArtifacts(ctx context.Context, workspaceID, kind string, limit int) ([]model.Artifact, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	query := r.db.WithContext(ctx).Where("workspace_id = ?", workspaceID)
	if strings.TrimSpace(kind) != "" {
		query = query.Where("kind = ?", kind)
	}
	var rows []artifactRow
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Artifact, len(rows))
	for i := range rows {
		out[i] = *rows[i].toModel()
	}
	return out, nil
}

type analysisResultRow struct {
	ID                    string          `gorm:"column:id;primaryKey"`
	WorkspaceID           string          `gorm:"column:workspace_id"`
	Instruction           string          `gorm:"column:instruction"`
	OutputContract        json.RawMessage `gorm:"column:output_contract;type:jsonb"`
	OutputContractHash    string          `gorm:"column:output_contract_hash"`
	OutputSchema          json.RawMessage `gorm:"column:output_schema;type:jsonb"`
	OutputSchemaHash      string          `gorm:"column:output_schema_hash"`
	ProducerWorkflowRunID string          `gorm:"column:producer_workflow_run_id"`
	ProducerNodeRunID     string          `gorm:"column:producer_node_run_id"`
	ProducerAttempt       int             `gorm:"column:producer_attempt"`
	Status                string          `gorm:"column:status"`
	Output                string          `gorm:"column:output"`
	AgentContext          string          `gorm:"column:agent_context"`
	Model                 string          `gorm:"column:model"`
	InputTokens           int             `gorm:"column:input_tokens"`
	OutputTokens          int             `gorm:"column:output_tokens"`
	CacheReadTokens       int             `gorm:"column:cache_read_tokens"`
	CacheWriteTokens      int             `gorm:"column:cache_write_tokens"`
	ErrorMessage          string          `gorm:"column:error_message"`
	CreatedAt             time.Time       `gorm:"column:created_at"`
	FinishedAt            *time.Time      `gorm:"column:finished_at"`
}

func (analysisResultRow) TableName() string { return "analysis_results" }
func (row analysisResultRow) toModel() *model.AnalysisResult {
	result := &model.AnalysisResult{ID: row.ID, WorkspaceID: row.WorkspaceID,
		Instruction: row.Instruction, OutputContract: row.OutputContract,
		OutputContractHash: row.OutputContractHash, OutputSchema: row.OutputSchema,
		OutputSchemaHash: row.OutputSchemaHash, ProducerWorkflowRunID: row.ProducerWorkflowRunID,
		ProducerNodeRunID: row.ProducerNodeRunID, ProducerAttempt: row.ProducerAttempt,
		Status: row.Status, Output: json.RawMessage(row.Output), AgentContext: json.RawMessage(row.AgentContext),
		Model: row.Model, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
		CacheReadTokens: row.CacheReadTokens, CacheWriteTokens: row.CacheWriteTokens,
		ErrorMessage: row.ErrorMessage, CreatedAt: row.CreatedAt}
	if row.FinishedAt != nil {
		result.FinishedAt = *row.FinishedAt
	}
	return result
}

type artifactRow struct {
	ID                    string    `gorm:"column:id;primaryKey"`
	WorkspaceID           string    `gorm:"column:workspace_id"`
	Kind                  string    `gorm:"column:kind"`
	Name                  string    `gorm:"column:name"`
	BlobURI               string    `gorm:"column:blob_uri"`
	ContentHash           string    `gorm:"column:content_hash"`
	ProducerWorkflowRunID string    `gorm:"column:producer_workflow_run_id"`
	ProducerNodeRunID     string    `gorm:"column:producer_node_run_id"`
	ProducerAttempt       int       `gorm:"column:producer_attempt"`
	Metadata              string    `gorm:"column:metadata"`
	CreatedAt             time.Time `gorm:"column:created_at"`
}

func (artifactRow) TableName() string { return "artifacts" }
func (row artifactRow) toModel() *model.Artifact {
	return &model.Artifact{ID: row.ID, WorkspaceID: row.WorkspaceID, Kind: row.Kind,
		Name: row.Name, BlobURI: row.BlobURI, ContentHash: row.ContentHash,
		ProducerWorkflowRunID: row.ProducerWorkflowRunID, ProducerNodeRunID: row.ProducerNodeRunID,
		ProducerAttempt: row.ProducerAttempt, Metadata: row.Metadata, CreatedAt: row.CreatedAt}
}
