package retrieval

import (
	"context"
	"errors"
	"fmt"
	"time"

	"reqflow/internal/domain/model"
)

// ErrProfileInUse 表示索引规则仍被索引快照引用，拒绝删除。
var ErrProfileInUse = errors.New("索引规则仍在使用中")

type CreateProfileRequest struct {
	WorkspaceID     string              `json:"workspace_id,omitempty"`
	Name            string              `json:"name"`
	DatasetSchemaID string              `json:"dataset_schema_id"`
	Lexical         model.LexicalConfig `json:"lexical"`
	Vector          model.VectorConfig  `json:"vector"`
	FilterFields    []string            `json:"filter_fields,omitempty"`
	Fusion          model.FusionConfig  `json:"fusion"`
}

type CloneProfileRequest struct {
	Name string `json:"name"`
}

type ProfileView struct {
	ID              string              `json:"id"`
	WorkspaceID     string              `json:"workspace_id"`
	Name            string              `json:"name"`
	DatasetSchemaID string              `json:"dataset_schema_id"`
	Lexical         model.LexicalConfig `json:"lexical"`
	Vector          model.VectorConfig  `json:"vector"`
	FilterFields    []string            `json:"filter_fields"`
	Fusion          model.FusionConfig  `json:"fusion"`
	ProfileHash     string              `json:"profile_hash"`
	CreatedAt       time.Time           `json:"created_at"`
}

func (s *Service) RegisterProfile(ctx context.Context, request CreateProfileRequest) (*ProfileView, error) {
	profile, err := s.CreateProfile(ctx, CreateProfileInput(request))
	if err != nil {
		return nil, err
	}
	view := profileView(profile)
	return &view, nil
}

func (s *Service) GetProfileView(ctx context.Context, id string) (*ProfileView, error) {
	profile, err := s.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	view := profileView(profile)
	return &view, nil
}

func (s *Service) ListProfileViews(ctx context.Context, workspaceID, datasetSchemaID string,
	limit int) ([]ProfileView, error) {
	if workspaceID == "" {
		workspaceID = "default"
	}
	profiles, err := s.repo.ListRetrievalProfiles(ctx, workspaceID, datasetSchemaID, limit)
	if err != nil {
		return nil, err
	}
	views := make([]ProfileView, len(profiles))
	for i := range profiles {
		views[i] = profileView(&profiles[i])
	}
	return views, nil
}

func (s *Service) CloneProfileView(ctx context.Context, id string, request CloneProfileRequest) (*ProfileView, error) {
	profile, err := s.CloneProfile(ctx, id, request.Name)
	if err != nil {
		return nil, err
	}
	view := profileView(profile)
	return &view, nil
}

// DeleteProfile 删除索引规则。已有快照的规则不允许删除——快照是流程任务产物，
// 保留后才能审计每次索引建立了什么；先在数据集索引抽屉删除快照即可解除引用。
// 删除规则时顺带清理残留的向量 Chunk；BM25 物理索引按规则 ID 命名且不复用，无需清理。
func (s *Service) DeleteProfile(ctx context.Context, id string) (bool, error) {
	profile, err := s.GetProfile(ctx, id)
	if err != nil {
		return false, err
	}
	count, err := s.repo.CountRetrievalSnapshotsByProfile(ctx, profile.ID)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, fmt.Errorf("%w：仍有 %d 个索引快照，请先在数据集索引抽屉删除对应快照", ErrProfileInUse, count)
	}
	if err := s.repo.DeleteRetrievalChunksByProfile(ctx, profile.ID); err != nil {
		return false, err
	}
	return s.repo.DeleteRetrievalProfile(ctx, profile.ID)
}

type SnapshotView struct {
	ID                 string    `json:"id"`
	DatasetID          string    `json:"dataset_id"`
	RetrievalProfileID string    `json:"retrieval_profile_id"`
	SourceStepRunID    string    `json:"source_step_run_id,omitempty"`
	SourceSeq          int64     `json:"source_seq"`
	Status             string    `json:"status"`
	LexicalRef         string    `json:"lexical_ref,omitempty"`
	VectorRef          string    `json:"vector_ref,omitempty"`
	LexicalCount       int       `json:"lexical_count"`
	VectorCount        int       `json:"vector_count"`
	FailureReason      string    `json:"failure_reason,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	ActivatedAt        time.Time `json:"activated_at,omitempty"`
}

// DeleteSnapshot 删除快照元数据行。物理索引按（数据集, 规则）共享且随增量构建自愈，
// 删除后下一次构建会自动从 0 全量重建，因此无需清理 LexicalRef / VectorRef。
func (s *Service) DeleteSnapshot(ctx context.Context, id string) (bool, error) {
	return s.repo.DeleteRetrievalSnapshot(ctx, id)
}

func (s *Service) GetSnapshotView(ctx context.Context, id string) (*SnapshotView, error) {
	snapshot, err := s.repo.GetRetrievalSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	view := snapshotView(snapshot)
	return &view, nil
}

func (s *Service) ListSnapshotViews(ctx context.Context, datasetID, profileID, status string,
	limit int) ([]SnapshotView, error) {
	snapshots, err := s.repo.ListRetrievalSnapshots(ctx, datasetID, profileID, status, limit)
	if err != nil {
		return nil, err
	}
	views := make([]SnapshotView, len(snapshots))
	for i := range snapshots {
		views[i] = snapshotView(&snapshots[i])
	}
	return views, nil
}

type SearchAPIRequest struct {
	RetrievalSnapshotID string                        `json:"retrieval_snapshot_id"`
	Query               string                        `json:"query"`
	Filters             map[string][]string           `json:"filters,omitempty"`
	Strategy            model.RetrievalSearchStrategy `json:"strategy"`
}

func (s *Service) SearchAPI(ctx context.Context, request SearchAPIRequest) (*SearchResponse, error) {
	return s.Search(ctx, SearchRequest(request))
}

func profileView(profile *model.RetrievalProfile) ProfileView {
	return ProfileView{ID: profile.ID, WorkspaceID: profile.WorkspaceID, Name: profile.Name,
		DatasetSchemaID: profile.DatasetSchemaID, Lexical: profile.Lexical, Vector: profile.Vector,
		FilterFields: profile.FilterFields, Fusion: profile.Fusion, ProfileHash: profile.ProfileHash,
		CreatedAt: profile.CreatedAt}
}

func snapshotView(snapshot *model.RetrievalSnapshot) SnapshotView {
	return SnapshotView{ID: snapshot.ID, DatasetID: snapshot.DatasetID,
		RetrievalProfileID: snapshot.RetrievalProfileID, SourceStepRunID: snapshot.SourceStepRunID,
		SourceSeq: snapshot.SourceSeq, Status: snapshot.Status, LexicalRef: snapshot.LexicalRef,
		VectorRef: snapshot.VectorRef, LexicalCount: snapshot.LexicalCount,
		VectorCount: snapshot.VectorCount, FailureReason: snapshot.FailureReason,
		CreatedAt: snapshot.CreatedAt, ActivatedAt: snapshot.ActivatedAt}
}
