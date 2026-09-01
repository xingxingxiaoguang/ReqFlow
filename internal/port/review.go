package port

import (
	"context"

	"reqflow/internal/domain/model"
)

// RecordReviewRepo 保存人工 Gate 产生的不可变审核结论。producer_node_run_id 是
// 幂等键：同一 Gate 的网络重试只能复用完全相同的 review_hash。
type RecordReviewRepo interface {
	CreateApprovedRecordSet(ctx context.Context, set *model.ApprovedRecordSet, decisions []model.RecordReviewDecision) (*model.ApprovedRecordSet, error)
	GetApprovedRecordSet(ctx context.Context, id string) (*model.ApprovedRecordSet, error)
	FindApprovedRecordSetByNodeRun(ctx context.Context, nodeRunID string) (*model.ApprovedRecordSet, bool, error)
	ListRecordReviewDecisions(ctx context.Context, setID string) ([]model.RecordReviewDecision, error)
}

type ReviewPipelineRepo interface {
	CleaningPipelineRepo
	RecordReviewRepo
}
