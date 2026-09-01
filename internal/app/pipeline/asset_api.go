package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"reqflow/internal/domain/model"
)

type UploadAssetRequest struct {
	WorkspaceID string
	Filename    string
	MIMEType    string
	SizeBytes   int64
	Content     io.Reader
}

type AssetView struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Filename    string    `json:"filename"`
	MIMEType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *AssetService) RegisterAsset(ctx context.Context, request UploadAssetRequest) (*AssetView, bool, error) {
	asset, created, err := s.UploadAsset(ctx, UploadAssetInput(request))
	if err != nil {
		return nil, false, err
	}
	view := assetView(asset)
	return &view, created, nil
}

type CreateAssetSetRequest struct {
	WorkspaceID string   `json:"workspace_id,omitempty"`
	Name        string   `json:"name"`
	CreatedBy   string   `json:"created_by,omitempty"`
	AssetIDs    []string `json:"asset_ids"`
}

type AssetSetMemberView struct {
	Ordinal int       `json:"ordinal"`
	Asset   AssetView `json:"asset"`
}

type AssetSetView struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	Name        string               `json:"name"`
	CreatedBy   string               `json:"created_by,omitempty"`
	Members     []AssetSetMemberView `json:"members"`
	CreatedAt   time.Time            `json:"created_at"`
}

func (s *AssetService) RegisterAssetSet(ctx context.Context, request CreateAssetSetRequest) (*AssetSetView, error) {
	set, entries, err := s.CreateAssetSet(ctx, CreateAssetSetInput(request))
	if err != nil {
		return nil, err
	}
	view := assetSetView(set.ID, set.WorkspaceID, set.Name, set.CreatedBy, set.CreatedAt, entries)
	return &view, nil
}

func (s *AssetService) ViewAssetSet(ctx context.Context, id string) (*AssetSetView, error) {
	set, entries, err := s.GetAssetSet(ctx, id)
	if err != nil {
		return nil, err
	}
	view := assetSetView(set.ID, set.WorkspaceID, set.Name, set.CreatedBy, set.CreatedAt, entries)
	return &view, nil
}

type ParsedDocumentSetItemView struct {
	Ordinal          int       `json:"ordinal"`
	Asset            AssetView `json:"asset"`
	Status           string    `json:"status"`
	ParsedDocumentID string    `json:"parsed_document_id,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
}

type ParsedDocumentSetView struct {
	ID                string                      `json:"id"`
	AssetSetID        string                      `json:"asset_set_id"`
	ProducerNodeRunID string                      `json:"producer_node_run_id"`
	ParserName        string                      `json:"parser_name"`
	ParserVersion     string                      `json:"parser_version"`
	Status            string                      `json:"status"`
	TotalCount        int                         `json:"total_count"`
	SucceededCount    int                         `json:"succeeded_count"`
	FailedCount       int                         `json:"failed_count"`
	Items             []ParsedDocumentSetItemView `json:"items"`
	CreatedAt         time.Time                   `json:"created_at"`
	FinishedAt        time.Time                   `json:"finished_at,omitempty"`
}

func (s *AssetService) ViewParsedDocumentSet(ctx context.Context, id string) (*ParsedDocumentSetView, error) {
	set, items, err := s.GetParsedDocumentSet(ctx, id)
	if err != nil {
		return nil, err
	}
	views := make([]ParsedDocumentSetItemView, len(items))
	for i, item := range items {
		asset, err := s.repo.GetAsset(ctx, item.AssetID)
		if err != nil {
			return nil, err
		}
		views[i] = ParsedDocumentSetItemView{Ordinal: item.Ordinal, Asset: assetView(asset),
			Status: item.Status, ParsedDocumentID: item.ParsedDocumentID, ErrorMessage: item.ErrorMessage}
	}
	return &ParsedDocumentSetView{ID: set.ID, AssetSetID: set.AssetSetID,
		ProducerNodeRunID: set.ProducerNodeRunID, ParserName: set.ParserName,
		ParserVersion: set.ParserVersion, Status: set.Status, TotalCount: set.TotalCount,
		SucceededCount: set.SucceededCount, FailedCount: set.FailedCount, Items: views,
		CreatedAt: set.CreatedAt, FinishedAt: set.FinishedAt}, nil
}

type DocumentBlockView struct {
	ID          string          `json:"id"`
	Ordinal     int             `json:"ordinal"`
	BlockType   string          `json:"block_type"`
	PageNo      int             `json:"page_no,omitempty"`
	SectionPath string          `json:"section_path,omitempty"`
	Text        string          `json:"text"`
	Metadata    json.RawMessage `json:"metadata"`
}

type ParsedDocumentView struct {
	ID            string              `json:"id"`
	AssetID       string              `json:"asset_id"`
	ParserName    string              `json:"parser_name"`
	ParserVersion string              `json:"parser_version"`
	Status        string              `json:"status"`
	ContentHash   string              `json:"content_hash"`
	Blocks        []DocumentBlockView `json:"blocks"`
	CreatedAt     time.Time           `json:"created_at"`
}

func (s *AssetService) ViewDocumentBlocks(ctx context.Context, id string, afterOrdinal, limit int) (*ParsedDocumentView, error) {
	document, blocks, err := s.GetDocumentBlocks(ctx, id, afterOrdinal, limit)
	if err != nil {
		return nil, err
	}
	views := make([]DocumentBlockView, len(blocks))
	for i, block := range blocks {
		views[i] = DocumentBlockView{ID: block.ID, Ordinal: block.Ordinal, BlockType: block.BlockType,
			PageNo: block.PageNo, SectionPath: block.SectionPath, Text: block.Text,
			Metadata: json.RawMessage(block.Metadata)}
	}
	return &ParsedDocumentView{ID: document.ID, AssetID: document.AssetID,
		ParserName: document.ParserName, ParserVersion: document.ParserVersion,
		Status: document.Status, ContentHash: document.ContentHash, Blocks: views,
		CreatedAt: document.CreatedAt}, nil
}

func assetView(asset *model.Asset) AssetView {
	return AssetView{ID: asset.ID, WorkspaceID: asset.WorkspaceID, Filename: asset.Filename,
		MIMEType: asset.MIMEType, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256,
		CreatedAt: asset.CreatedAt}
}

func assetSetView(id, workspaceID, name, createdBy string, createdAt time.Time, entries []model.AssetSetEntry) AssetSetView {
	members := make([]AssetSetMemberView, len(entries))
	for i := range entries {
		asset := entries[i].Asset
		members[i] = AssetSetMemberView{Ordinal: entries[i].Member.Ordinal, Asset: assetView(&asset)}
	}
	return AssetSetView{ID: id, WorkspaceID: workspaceID, Name: name, CreatedBy: createdBy,
		Members: members, CreatedAt: createdAt}
}
