package extraction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"reqflow/internal/domain/model"
)

// ErrProfileInUse 表示抽取规则仍被历史抽取产物引用，拒绝删除。
var ErrProfileInUse = errors.New("抽取规则仍在使用中")

type CreateExtractionProfileRequest struct {
	WorkspaceID        string          `json:"workspace_id,omitempty"`
	Name               string          `json:"name"`
	TargetSchemaID     string          `json:"target_schema_id"`
	RecordGranularity  string          `json:"record_granularity"`
	SystemInstruction  string          `json:"system_instruction"`
	FieldGuides        json.RawMessage `json:"field_guides,omitempty"`
	Examples           json.RawMessage `json:"examples,omitempty"`
	NormalizationRules json.RawMessage `json:"normalization_rules,omitempty"`
	ValidationRules    json.RawMessage `json:"validation_rules,omitempty"`
}

type ExtractionProfileView struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	Name               string          `json:"name"`
	TargetSchemaID     string          `json:"target_schema_id"`
	RecordGranularity  string          `json:"record_granularity"`
	SystemInstruction  string          `json:"system_instruction"`
	FieldGuides        json.RawMessage `json:"field_guides"`
	Examples           json.RawMessage `json:"examples"`
	NormalizationRules json.RawMessage `json:"normalization_rules"`
	ValidationRules    json.RawMessage `json:"validation_rules"`
	ProfileHash        string          `json:"profile_hash"`
	CreatedAt          time.Time       `json:"created_at"`
}

func (s *Service) RegisterProfile(ctx context.Context, request CreateExtractionProfileRequest) (*ExtractionProfileView, error) {
	profile, err := s.CreateProfile(ctx, CreateExtractionProfileInput(request))
	if err != nil {
		return nil, err
	}
	view := extractionProfileView(profile)
	return &view, nil
}

func (s *Service) ViewProfile(ctx context.Context, id string) (*ExtractionProfileView, error) {
	profile, err := s.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	view := extractionProfileView(profile)
	return &view, nil
}

// DeleteProfile 删除抽取规则。已被任务产物（草稿集/转换集）引用的规则不可删除，
// 保证历史任务与已入库数据的抽取血缘可追溯；未使用过的规则可直接删除。
func (s *Service) DeleteProfile(ctx context.Context, id string) (bool, error) {
	profile, err := s.GetProfile(ctx, id)
	if err != nil {
		return false, err
	}
	usage, err := s.repo.CountExtractionProfileUsage(ctx, profile.ID)
	if err != nil {
		return false, err
	}
	if usage > 0 {
		return false, fmt.Errorf("%w：已被 %d 个历史抽取产物引用，无法删除", ErrProfileInUse, usage)
	}
	return s.repo.DeleteExtractionProfile(ctx, profile.ID)
}

type ExtractionUnitView struct {
	ID                string    `json:"id"`
	UnitKey           string    `json:"unit_key"`
	ParsedDocumentID  string    `json:"parsed_document_id"`
	Ordinal           int       `json:"ordinal"`
	FirstBlockOrdinal int       `json:"first_block_ordinal"`
	LastBlockOrdinal  int       `json:"last_block_ordinal"`
	InputHash         string    `json:"input_hash"`
	Status            string    `json:"status"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	ResponseHash      string    `json:"response_hash,omitempty"`
	RequestCount      int       `json:"request_count"`
	InputTokens       int       `json:"input_tokens"`
	OutputTokens      int       `json:"output_tokens"`
	CacheReadTokens   int       `json:"cache_read_tokens"`
	CacheWriteTokens  int       `json:"cache_write_tokens"`
	CreatedAt         time.Time `json:"created_at"`
	FinishedAt        time.Time `json:"finished_at,omitempty"`
}

type RecordDraftView struct {
	ID               string               `json:"id"`
	ExtractionUnitID string               `json:"extraction_unit_id"`
	Ordinal          int                  `json:"ordinal"`
	Fields           json.RawMessage      `json:"fields"`
	FieldConfidence  json.RawMessage      `json:"field_confidence"`
	Provenance       model.ItemProvenance `json:"provenance"`
	CreatedAt        time.Time            `json:"created_at"`
}

type RecordDraftSetView struct {
	ID                  string               `json:"id"`
	ParsedDocumentSetID string               `json:"parsed_document_set_id"`
	ExtractionProfileID string               `json:"extraction_profile_id"`
	SourceStepRunID     string               `json:"source_step_run_id"`
	Status              string               `json:"status"`
	Model               string               `json:"model"`
	UnitCount           int                  `json:"unit_count"`
	SucceededUnitCount  int                  `json:"succeeded_unit_count"`
	FailedUnitCount     int                  `json:"failed_unit_count"`
	DraftCount          int                  `json:"draft_count"`
	LLMRequestCount     int                  `json:"llm_request_count"`
	InputTokens         int                  `json:"input_tokens"`
	OutputTokens        int                  `json:"output_tokens"`
	CacheReadTokens     int                  `json:"cache_read_tokens"`
	CacheWriteTokens    int                  `json:"cache_write_tokens"`
	Units               []ExtractionUnitView `json:"units"`
	Drafts              []RecordDraftView    `json:"drafts"`
	CreatedAt           time.Time            `json:"created_at"`
	FinishedAt          time.Time            `json:"finished_at,omitempty"`
}

func (s *Service) ViewRecordDraftSet(ctx context.Context, id string) (*RecordDraftSetView, error) {
	set, units, drafts, err := s.GetRecordDraftSet(ctx, id)
	if err != nil {
		return nil, err
	}
	unitViews := make([]ExtractionUnitView, len(units))
	for i, unit := range units {
		unitViews[i] = ExtractionUnitView{ID: unit.ID, UnitKey: unit.UnitKey,
			ParsedDocumentID: unit.ParsedDocumentID, Ordinal: unit.Ordinal,
			FirstBlockOrdinal: unit.FirstBlockOrdinal, LastBlockOrdinal: unit.LastBlockOrdinal,
			InputHash: unit.InputHash, Status: unit.Status, ErrorMessage: unit.ErrorMessage,
			ResponseHash: unit.ResponseHash, RequestCount: unit.RequestCount,
			InputTokens: unit.InputTokens, OutputTokens: unit.OutputTokens,
			CacheReadTokens: unit.CacheReadTokens, CacheWriteTokens: unit.CacheWriteTokens,
			CreatedAt: unit.CreatedAt, FinishedAt: unit.FinishedAt}
	}
	draftViews := make([]RecordDraftView, len(drafts))
	for i, draft := range drafts {
		draftViews[i] = RecordDraftView{ID: draft.ID, ExtractionUnitID: draft.ExtractionUnitID,
			Ordinal: draft.Ordinal, Fields: draft.Fields, FieldConfidence: draft.FieldConfidence,
			Provenance: draft.Provenance, CreatedAt: draft.CreatedAt}
	}
	return &RecordDraftSetView{ID: set.ID, ParsedDocumentSetID: set.ParsedDocumentSetID,
		ExtractionProfileID: set.ExtractionProfileID, SourceStepRunID: set.SourceStepRunID,
		Status: set.Status, Model: set.Model, UnitCount: set.UnitCount,
		SucceededUnitCount: set.SucceededUnitCount, FailedUnitCount: set.FailedUnitCount,
		DraftCount: set.DraftCount, LLMRequestCount: set.LLMRequestCount,
		InputTokens: set.InputTokens, OutputTokens: set.OutputTokens,
		CacheReadTokens: set.CacheReadTokens, CacheWriteTokens: set.CacheWriteTokens,
		Units: unitViews, Drafts: draftViews,
		CreatedAt: set.CreatedAt, FinishedAt: set.FinishedAt}, nil
}

func extractionProfileView(profile *model.ExtractionProfile) ExtractionProfileView {
	return ExtractionProfileView{ID: profile.ID, WorkspaceID: profile.WorkspaceID,
		Name: profile.Name, TargetSchemaID: profile.TargetSchemaID,
		RecordGranularity: profile.RecordGranularity, SystemInstruction: profile.SystemInstruction,
		FieldGuides: profile.FieldGuides, Examples: profile.Examples,
		NormalizationRules: profile.NormalizationRules, ValidationRules: profile.ValidationRules,
		ProfileHash: profile.ProfileHash, CreatedAt: profile.CreatedAt}
}
