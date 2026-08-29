package port

import (
	"context"
	"errors"
	"io"

	"reqflow/internal/domain/model"
)

var (
	ErrResourceNotFound       = errors.New("resource not found")
	ErrStaleResourceExecution = errors.New("stale resource execution")
)

// BlobObject 是内容寻址存储的写入结果。URI 是不透明定位符，应用层不得把它
// 当成本地路径拼接；SHA256 是小写十六进制内容摘要。
type BlobObject struct {
	URI       string
	SHA256    string
	SizeBytes int64
}

// BlobStore 保存不可变原始内容。相同内容的 Put 必须幂等。
type BlobStore interface {
	Put(ctx context.Context, content io.Reader) (BlobObject, error)
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

type AssetCatalogRepo interface {
	CreateOrGetAsset(ctx context.Context, asset *model.Asset) (*model.Asset, bool, error)
	GetAsset(ctx context.Context, id string) (*model.Asset, error)
	CreateAssetSet(ctx context.Context, set *model.AssetSet, members []model.AssetSetMember) error
	GetAssetSet(ctx context.Context, id string) (*model.AssetSet, error)
	ListAssetSetEntries(ctx context.Context, assetSetID string) ([]model.AssetSetEntry, error)
}

type ParsedDocumentRepo interface {
	GetParsedDocument(ctx context.Context, assetID, parserName, parserVersion string) (*model.ParsedDocument, []model.DocumentBlock, error)
	GetParsedDocumentByID(ctx context.Context, id string) (*model.ParsedDocument, error)
	ListDocumentBlocks(ctx context.Context, parsedDocumentID string, afterOrdinal, limit int) ([]model.DocumentBlock, error)
	SaveParsedDocument(ctx context.Context, document *model.ParsedDocument, blocks []model.DocumentBlock) (*model.ParsedDocument, error)

	BeginParsedDocumentSet(ctx context.Context, set *model.ParsedDocumentSet, members []model.ParsedDocumentSetItem) (*model.ParsedDocumentSet, error)
	RecordParsedDocumentSetItem(ctx context.Context, setID string, producerAttempt int, item model.ParsedDocumentSetItem) error
	FinalizeParsedDocumentSet(ctx context.Context, setID string, producerAttempt int) (*model.ParsedDocumentSet, error)
	GetParsedDocumentSet(ctx context.Context, id string) (*model.ParsedDocumentSet, []model.ParsedDocumentSetItem, error)
}

type AssetPipelineRepo interface {
	AssetCatalogRepo
	ParsedDocumentRepo
}
