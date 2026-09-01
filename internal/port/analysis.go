package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// AnalysisRepo 保存自包含、可恢复的 Agent 运行结果和正式 Artifact。
// 所有节点产物写入都由 producer_node_run_id + producer_attempt fencing。
type AnalysisRepo interface {
	BeginAnalysisResult(ctx context.Context, result *model.AnalysisResult, producerAttempt int) (*model.AnalysisResult, error)
	GetAnalysisResult(ctx context.Context, id string) (*model.AnalysisResult, error)
	CompleteAnalysisResult(ctx context.Context, result *model.AnalysisResult, producerAttempt int) error
	FailAnalysisResult(ctx context.Context, id, nodeRunID string, producerAttempt int, message string) error

	CreateArtifactForNode(ctx context.Context, artifact *model.Artifact, producerAttempt int) (*model.Artifact, error)
	GetArtifact(ctx context.Context, id string) (*model.Artifact, error)
	ListArtifacts(ctx context.Context, workspaceID, kind string, limit int) ([]model.Artifact, error)
}
