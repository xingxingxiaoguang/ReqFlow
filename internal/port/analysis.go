package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// AnalysisRepo 保存不可变 Profile、可恢复的 Agent 运行结果和正式 Artifact。
// 所有 Step 产物写入都由 source_step_run_id + producer_attempt fencing。
type AnalysisRepo interface {
	CreateAnalysisProfile(ctx context.Context, profile *model.AnalysisProfile) error
	GetAnalysisProfile(ctx context.Context, id string) (*model.AnalysisProfile, error)
	ListAnalysisProfiles(ctx context.Context, workspaceID string, limit int) ([]model.AnalysisProfile, error)

	BeginAnalysisResult(ctx context.Context, result *model.AnalysisResult, producerAttempt int) (*model.AnalysisResult, error)
	GetAnalysisResult(ctx context.Context, id string) (*model.AnalysisResult, error)
	CompleteAnalysisResult(ctx context.Context, result *model.AnalysisResult, producerAttempt int) error
	FailAnalysisResult(ctx context.Context, id, stepRunID string, producerAttempt int, message string) error

	CreateArtifactForStep(ctx context.Context, artifact *model.Artifact, producerAttempt int) (*model.Artifact, error)
	GetArtifact(ctx context.Context, id string) (*model.Artifact, error)
	ListArtifacts(ctx context.Context, workspaceID, kind string, limit int) ([]model.Artifact, error)
}
