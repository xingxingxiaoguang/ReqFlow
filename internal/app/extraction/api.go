package extraction

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

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
	DataContract        json.RawMessage      `json:"data_contract"`
	DataContractHash    string               `json:"data_contract_hash"`
	ExtractionSpec      json.RawMessage      `json:"extraction_spec"`
	ExtractionSpecHash  string               `json:"extraction_spec_hash"`
	JSONSchema          json.RawMessage      `json:"json_schema"`
	SchemaHash          string               `json:"schema_hash"`
	ProducerNodeRunID   string               `json:"producer_node_run_id"`
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
		DataContract: set.DataContract, DataContractHash: set.DataContractHash,
		ExtractionSpec: set.ExtractionSpec, ExtractionSpecHash: set.ExtractionSpecHash,
		JSONSchema: set.JSONSchema, SchemaHash: set.SchemaHash, ProducerNodeRunID: set.ProducerNodeRunID,
		Status: set.Status, Model: set.Model, UnitCount: set.UnitCount,
		SucceededUnitCount: set.SucceededUnitCount, FailedUnitCount: set.FailedUnitCount,
		DraftCount: set.DraftCount, LLMRequestCount: set.LLMRequestCount,
		InputTokens: set.InputTokens, OutputTokens: set.OutputTokens,
		CacheReadTokens: set.CacheReadTokens, CacheWriteTokens: set.CacheWriteTokens,
		Units: unitViews, Drafts: draftViews,
		CreatedAt: set.CreatedAt, FinishedAt: set.FinishedAt}, nil
}
