package analysis

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

type CreateProfileRequest struct {
	WorkspaceID  string          `json:"workspace_id,omitempty"`
	Name         string          `json:"name"`
	Instruction  string          `json:"instruction"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

type CloneProfileRequest struct {
	Name string `json:"name"`
}

type ProfileView struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id"`
	Name         string          `json:"name"`
	Instruction  string          `json:"instruction"`
	OutputSchema json.RawMessage `json:"output_schema"`
	ProfileHash  string          `json:"profile_hash"`
	CreatedAt    time.Time       `json:"created_at"`
}

type ResultView struct {
	ID                    string          `json:"id"`
	WorkspaceID           string          `json:"workspace_id"`
	AnalysisProfileID     string          `json:"analysis_profile_id"`
	ProducerWorkflowRunID string          `json:"producer_workflow_run_id"`
	ProducerNodeRunID     string          `json:"producer_node_run_id"`
	Status                string          `json:"status"`
	Output                json.RawMessage `json:"output"`
	Model                 string          `json:"model"`
	InputTokens           int             `json:"input_tokens"`
	OutputTokens          int             `json:"output_tokens"`
	CacheReadTokens       int             `json:"cache_read_tokens"`
	CacheWriteTokens      int             `json:"cache_write_tokens"`
	ErrorMessage          string          `json:"error_message,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	FinishedAt            time.Time       `json:"finished_at,omitempty"`
}

type ArtifactView struct {
	ID                    string          `json:"id"`
	WorkspaceID           string          `json:"workspace_id"`
	Kind                  string          `json:"kind"`
	Name                  string          `json:"name"`
	ContentHash           string          `json:"content_hash"`
	ProducerWorkflowRunID string          `json:"producer_workflow_run_id"`
	ProducerNodeRunID     string          `json:"producer_node_run_id"`
	Metadata              json.RawMessage `json:"metadata"`
	CreatedAt             time.Time       `json:"created_at"`
}

func (s *Service) RegisterProfile(ctx context.Context, request CreateProfileRequest) (*ProfileView, error) {
	profile, err := s.CreateProfile(ctx, CreateProfileInput(request))
	if err != nil {
		return nil, err
	}
	view := profileView(*profile)
	return &view, nil
}

func (s *Service) GetProfileView(ctx context.Context, id string) (*ProfileView, error) {
	profile, err := s.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	view := profileView(*profile)
	return &view, nil
}

func (s *Service) ListProfileViews(ctx context.Context, workspaceID string, limit int) ([]ProfileView, error) {
	profiles, err := s.ListProfiles(ctx, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	views := make([]ProfileView, len(profiles))
	for i := range profiles {
		views[i] = profileView(profiles[i])
	}
	return views, nil
}

func (s *Service) CloneProfileView(ctx context.Context, id string, request CloneProfileRequest) (*ProfileView, error) {
	profile, err := s.CloneProfile(ctx, id, request.Name)
	if err != nil {
		return nil, err
	}
	view := profileView(*profile)
	return &view, nil
}

func (s *Service) GetResultView(ctx context.Context, id string) (*ResultView, error) {
	result, err := s.GetResult(ctx, id)
	if err != nil {
		return nil, err
	}
	view := resultView(*result)
	return &view, nil
}

func (s *ArtifactService) GetView(ctx context.Context, id string) (*ArtifactView, error) {
	artifact, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	view := artifactView(*artifact)
	return &view, nil
}

func (s *ArtifactService) ListViews(ctx context.Context, workspaceID, kind string, limit int) ([]ArtifactView, error) {
	artifacts, err := s.List(ctx, workspaceID, kind, limit)
	if err != nil {
		return nil, err
	}
	views := make([]ArtifactView, len(artifacts))
	for i := range artifacts {
		views[i] = artifactView(artifacts[i])
	}
	return views, nil
}

func profileView(profile model.AnalysisProfile) ProfileView {
	return ProfileView{ID: profile.ID, WorkspaceID: profile.WorkspaceID, Name: profile.Name,
		Instruction: profile.Instruction, OutputSchema: profile.OutputSchema,
		ProfileHash: profile.ProfileHash, CreatedAt: profile.CreatedAt}
}

func resultView(result model.AnalysisResult) ResultView {
	return ResultView{ID: result.ID, WorkspaceID: result.WorkspaceID,
		AnalysisProfileID: result.AnalysisProfileID, ProducerWorkflowRunID: result.ProducerWorkflowRunID,
		ProducerNodeRunID: result.ProducerNodeRunID, Status: result.Status, Output: result.Output,
		Model: result.Model, InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		CacheReadTokens: result.CacheReadTokens, CacheWriteTokens: result.CacheWriteTokens,
		ErrorMessage: result.ErrorMessage, CreatedAt: result.CreatedAt, FinishedAt: result.FinishedAt}
}

func artifactView(artifact model.Artifact) ArtifactView {
	metadata := json.RawMessage(artifact.Metadata)
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	return ArtifactView{ID: artifact.ID, WorkspaceID: artifact.WorkspaceID, Kind: artifact.Kind,
		Name: artifact.Name, ContentHash: artifact.ContentHash, ProducerWorkflowRunID: artifact.ProducerWorkflowRunID,
		ProducerNodeRunID: artifact.ProducerNodeRunID, Metadata: metadata, CreatedAt: artifact.CreatedAt}
}
