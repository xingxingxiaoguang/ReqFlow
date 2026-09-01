package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

var errAssetTooLarge = errors.New("asset exceeds size limit")

type AssetService struct {
	repo         port.AssetPipelineRepo
	blobs        port.BlobStore
	parser       port.DocumentParser
	maxFileBytes int64
}

func NewAssetService(repo port.AssetPipelineRepo, blobs port.BlobStore, parser port.DocumentParser, maxFileBytes int64) (*AssetService, error) {
	if repo == nil || blobs == nil || parser == nil {
		return nil, fmt.Errorf("asset pipeline: repo, blob store and parser are required")
	}
	if maxFileBytes <= 0 {
		maxFileBytes = 50 << 20
	}
	return &AssetService{repo: repo, blobs: blobs, parser: parser, maxFileBytes: maxFileBytes}, nil
}

type UploadAssetInput struct {
	WorkspaceID string
	Filename    string
	MIMEType    string
	SizeBytes   int64
	Content     io.Reader
}

func (s *AssetService) UploadAsset(ctx context.Context, in UploadAssetInput) (*model.Asset, bool, error) {
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	filename := safeFilename(in.Filename)
	if filename == "" {
		return nil, false, fmt.Errorf("文件名不能为空")
	}
	if in.Content == nil {
		return nil, false, fmt.Errorf("文件内容不能为空")
	}
	if in.SizeBytes > s.maxFileBytes {
		return nil, false, fmt.Errorf("文件超过大小限制 %dMB", s.maxFileBytes>>20)
	}
	object, err := s.blobs.Put(ctx, &maxBytesReader{reader: in.Content, remaining: s.maxFileBytes})
	if err != nil {
		if errors.Is(err, errAssetTooLarge) {
			return nil, false, fmt.Errorf("文件超过大小限制 %dMB", s.maxFileBytes>>20)
		}
		return nil, false, err
	}
	mimeType := strings.TrimSpace(in.MIMEType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(path.Ext(filename)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	asset := &model.Asset{WorkspaceID: workspaceID, Filename: filename, MIMEType: mimeType,
		SizeBytes: object.SizeBytes, SHA256: object.SHA256, BlobURI: object.URI}
	return s.repo.CreateOrGetAsset(ctx, asset)
}

type CreateAssetSetInput struct {
	WorkspaceID string
	Name        string
	CreatedBy   string
	AssetIDs    []string
}

func (s *AssetService) CreateAssetSet(ctx context.Context, in CreateAssetSetInput) (*model.AssetSet, []model.AssetSetEntry, error) {
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, nil, fmt.Errorf("AssetSet 名称不能为空")
	}
	if len(in.AssetIDs) == 0 {
		return nil, nil, fmt.Errorf("AssetSet 至少包含一个 Asset")
	}
	seen := make(map[string]struct{}, len(in.AssetIDs))
	members := make([]model.AssetSetMember, len(in.AssetIDs))
	for i, rawID := range in.AssetIDs {
		assetID := strings.TrimSpace(rawID)
		if assetID == "" {
			return nil, nil, fmt.Errorf("第 %d 个 asset_id 为空", i+1)
		}
		if _, exists := seen[assetID]; exists {
			return nil, nil, fmt.Errorf("AssetSet 不能重复引用 Asset %s", assetID)
		}
		seen[assetID] = struct{}{}
		members[i] = model.AssetSetMember{AssetID: assetID, Ordinal: i}
	}
	set := &model.AssetSet{WorkspaceID: workspaceID, Name: name, CreatedBy: strings.TrimSpace(in.CreatedBy)}
	if err := s.repo.CreateAssetSet(ctx, set, members); err != nil {
		return nil, nil, err
	}
	entries, err := s.repo.ListAssetSetEntries(ctx, set.ID)
	if err != nil {
		return nil, nil, err
	}
	return set, entries, nil
}

func (s *AssetService) GetAssetSet(ctx context.Context, id string) (*model.AssetSet, []model.AssetSetEntry, error) {
	set, err := s.repo.GetAssetSet(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	entries, err := s.repo.ListAssetSetEntries(ctx, id)
	return set, entries, err
}

func (s *AssetService) CreateSingleAssetSet(ctx context.Context, workspaceID, name, createdBy, assetID string) (*model.AssetSet, error) {
	set, _, err := s.CreateAssetSet(ctx, CreateAssetSetInput{
		WorkspaceID: workspaceID,
		Name:        name,
		CreatedBy:   createdBy,
		AssetIDs:    []string{assetID},
	})
	return set, err
}

type ParseAssetSetInput struct {
	AssetSetID        string
	ProducerNodeRunID string
	ProducerAttempt   int
}

type WorkflowParseInput struct {
	ResourceID  string
	ExecutionID string
	Attempt     int
}

func (s *AssetService) ParseWorkflowAssetSet(ctx context.Context, input WorkflowParseInput, onItem func(ParseAssetSetProgress) error) (*model.ParsedDocumentSet, error) {
	return s.ParseAssetSet(ctx, ParseAssetSetInput{AssetSetID: input.ResourceID,
		ProducerNodeRunID: input.ExecutionID, ProducerAttempt: input.Attempt}, onItem)
}

type ParseAssetSetProgress struct {
	ManifestID string
	AssetID    string
	Ordinal    int
	Total      int
	Completed  int
	Succeeded  int
	Failed     int
	Status     string
}

func (s *AssetService) ParseAssetSet(ctx context.Context, in ParseAssetSetInput, onItem func(ParseAssetSetProgress) error) (*model.ParsedDocumentSet, error) {
	if strings.TrimSpace(in.ProducerNodeRunID) == "" || in.ProducerAttempt <= 0 {
		return nil, fmt.Errorf("producer_node_run_id 和 producer_attempt 必须有效")
	}
	if _, err := s.repo.GetAssetSet(ctx, in.AssetSetID); err != nil {
		return nil, fmt.Errorf("读取 AssetSet: %w", err)
	}
	entries, err := s.repo.ListAssetSetEntries(ctx, in.AssetSetID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("AssetSet 不包含可解析资产")
	}
	members := make([]model.ParsedDocumentSetItem, len(entries))
	for i, entry := range entries {
		members[i] = model.ParsedDocumentSetItem{AssetID: entry.Asset.ID, Ordinal: entry.Member.Ordinal,
			Status: model.ParsedDocumentPending}
	}
	manifest, err := s.repo.BeginParsedDocumentSet(ctx, &model.ParsedDocumentSet{
		AssetSetID: in.AssetSetID, ProducerNodeRunID: in.ProducerNodeRunID,
		ParserName: s.parser.ParserName(), ParserVersion: s.parser.ParserVersion(),
		Status: model.ParsedDocumentSetRunning, ProducerAttempt: in.ProducerAttempt,
	}, members)
	if err != nil {
		return nil, err
	}
	_, existing, err := s.repo.GetParsedDocumentSet(ctx, manifest.ID)
	if err != nil {
		return nil, err
	}
	byAsset := make(map[string]model.ParsedDocumentSetItem, len(existing))
	succeeded, failed := 0, 0
	for _, item := range existing {
		byAsset[item.AssetID] = item
		if item.Status == model.ParsedDocumentSucceeded {
			succeeded++
		}
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if byAsset[entry.Asset.ID].Status == model.ParsedDocumentSucceeded {
			continue
		}
		document, _, getErr := s.repo.GetParsedDocument(ctx, entry.Asset.ID,
			s.parser.ParserName(), s.parser.ParserVersion())
		if getErr == nil && document.Status == model.ParsedDocumentSucceeded {
			err = s.repo.RecordParsedDocumentSetItem(ctx, manifest.ID, in.ProducerAttempt,
				model.ParsedDocumentSetItem{AssetID: entry.Asset.ID, ParsedDocumentID: document.ID,
					Ordinal: entry.Member.Ordinal, Status: model.ParsedDocumentSucceeded})
		} else {
			if getErr != nil && !errors.Is(getErr, port.ErrResourceNotFound) {
				return nil, getErr
			}
			document, err = s.parseAsset(ctx, entry.Asset)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				failedDocument, saveErr := s.repo.SaveParsedDocument(ctx, &model.ParsedDocument{
					AssetID: entry.Asset.ID, ParserName: s.parser.ParserName(), ParserVersion: s.parser.ParserVersion(),
					Status: model.ParsedDocumentFailed, ErrorMessage: err.Error(),
				}, nil)
				if saveErr != nil {
					return nil, saveErr
				}
				if failedDocument.Status == model.ParsedDocumentSucceeded {
					err = s.repo.RecordParsedDocumentSetItem(ctx, manifest.ID, in.ProducerAttempt,
						model.ParsedDocumentSetItem{AssetID: entry.Asset.ID, ParsedDocumentID: failedDocument.ID,
							Ordinal: entry.Member.Ordinal, Status: model.ParsedDocumentSucceeded})
				} else {
					err = s.repo.RecordParsedDocumentSetItem(ctx, manifest.ID, in.ProducerAttempt,
						model.ParsedDocumentSetItem{AssetID: entry.Asset.ID, Ordinal: entry.Member.Ordinal,
							Status: model.ParsedDocumentFailed, ErrorMessage: failedDocument.ErrorMessage})
				}
			} else {
				err = s.repo.RecordParsedDocumentSetItem(ctx, manifest.ID, in.ProducerAttempt,
					model.ParsedDocumentSetItem{AssetID: entry.Asset.ID, ParsedDocumentID: document.ID,
						Ordinal: entry.Member.Ordinal, Status: model.ParsedDocumentSucceeded})
			}
		}
		if err != nil {
			return nil, err
		}
		_, current, readErr := s.repo.GetParsedDocumentSet(ctx, manifest.ID)
		if readErr != nil {
			return nil, readErr
		}
		succeeded, failed = countParseResults(current)
		if onItem != nil {
			status := byStatus(current, entry.Asset.ID)
			if err := onItem(ParseAssetSetProgress{ManifestID: manifest.ID, AssetID: entry.Asset.ID,
				Ordinal: entry.Member.Ordinal, Total: len(entries), Completed: succeeded + failed,
				Succeeded: succeeded, Failed: failed, Status: status}); err != nil {
				return nil, err
			}
		}
	}
	return s.repo.FinalizeParsedDocumentSet(ctx, manifest.ID, in.ProducerAttempt)
}

func (s *AssetService) parseAsset(ctx context.Context, asset model.Asset) (*model.ParsedDocument, error) {
	reader, err := s.blobs.Open(ctx, asset.BlobURI)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	blocks, err := s.parser.Parse(ctx, port.ParseSource{Filename: asset.Filename,
		MIMEType: asset.MIMEType, SizeBytes: asset.SizeBytes, Content: reader}, nil)
	if err != nil {
		return nil, err
	}
	hash, err := parsedContentHash(blocks)
	if err != nil {
		return nil, err
	}
	return s.repo.SaveParsedDocument(ctx, &model.ParsedDocument{AssetID: asset.ID,
		ParserName: s.parser.ParserName(), ParserVersion: s.parser.ParserVersion(),
		Status: model.ParsedDocumentSucceeded, ContentHash: hash}, blocks)
}

func (s *AssetService) GetParsedDocumentSet(ctx context.Context, id string) (*model.ParsedDocumentSet, []model.ParsedDocumentSetItem, error) {
	return s.repo.GetParsedDocumentSet(ctx, id)
}

func (s *AssetService) GetDocumentBlocks(ctx context.Context, id string, afterOrdinal, limit int) (*model.ParsedDocument, []model.DocumentBlock, error) {
	document, err := s.repo.GetParsedDocumentByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	blocks, err := s.repo.ListDocumentBlocks(ctx, id, afterOrdinal, limit)
	return document, blocks, err
}

func parsedContentHash(blocks []model.DocumentBlock) (string, error) {
	type hashBlock struct {
		Ordinal     int             `json:"ordinal"`
		BlockType   string          `json:"block_type"`
		PageNo      int             `json:"page_no"`
		SectionPath string          `json:"section_path"`
		Text        string          `json:"text"`
		Metadata    json.RawMessage `json:"metadata"`
	}
	canonical := make([]hashBlock, len(blocks))
	for i := range blocks {
		var metadata any
		if err := json.Unmarshal([]byte(blocks[i].Metadata), &metadata); err != nil {
			return "", fmt.Errorf("第 %d 个 DocumentBlock metadata 非法: %w", i, err)
		}
		raw, _ := json.Marshal(metadata)
		canonical[i] = hashBlock{Ordinal: i, BlockType: blocks[i].BlockType, PageNo: blocks[i].PageNo,
			SectionPath: blocks[i].SectionPath, Text: blocks[i].Text, Metadata: raw}
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func safeFilename(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	value = path.Base(value)
	if value == "." || value == "/" || value == "" {
		return ""
	}
	return value
}

func countParseResults(items []model.ParsedDocumentSetItem) (succeeded, failed int) {
	for _, item := range items {
		switch item.Status {
		case model.ParsedDocumentSucceeded:
			succeeded++
		case model.ParsedDocumentFailed:
			failed++
		}
	}
	return succeeded, failed
}

func byStatus(items []model.ParsedDocumentSetItem, assetID string) string {
	for _, item := range items {
		if item.AssetID == assetID {
			return item.Status
		}
	}
	return model.ParsedDocumentPending
}

type maxBytesReader struct {
	reader    io.Reader
	remaining int64
}

func (r *maxBytesReader) Read(p []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(p)) > r.remaining {
			p = p[:r.remaining]
		}
		n, err := r.reader.Read(p)
		r.remaining -= int64(n)
		return n, err
	}
	var probe [1]byte
	n, err := r.reader.Read(probe[:])
	if n > 0 {
		return 0, errAssetTooLarge
	}
	return 0, err
}
