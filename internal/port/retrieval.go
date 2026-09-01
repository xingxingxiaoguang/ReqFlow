package port

import (
	"context"
	"encoding/json"
	"errors"

	"reqflow/internal/domain/model"
)

var ErrRetrievalSnapshotNotFound = errors.New("retrieval snapshot not found")

// RankedHit 是词法/向量后端的统一候选。后端原始分数只用于诊断，跨后端融合按排名进行。
type RankedHit struct {
	DatasetItemID string
	ChunkID       string
	Text          string
	Rank          int
	Score         float64
}

type LexicalDocument struct {
	DatasetItemID string
	SourceSeq     int64
	Fields        map[string]any
	Filters       map[string]any
}

type LexicalBuildRequest struct {
	IndexRef  string
	Analyzer  string
	Fields    map[string]float64
	Filters   []string
	Documents []LexicalDocument
}

type LexicalSearchRequest struct {
	IndexRef  string
	Query     string
	Fields    map[string]float64
	Filters   map[string][]string
	SourceSeq int64
	Limit     int
}

// LexicalBackend 是 BM25 物理后端边界。Build 必须按 DatasetItemID 幂等 upsert。
type LexicalBackend interface {
	Available() bool
	Build(ctx context.Context, req LexicalBuildRequest) error
	Count(ctx context.Context, indexRef string, sourceSeq int64) (int, error)
	Search(ctx context.Context, req LexicalSearchRequest) ([]RankedHit, error)
}

type VectorSearchRequest struct {
	DatasetID          string
	RetrievalProfileID string
	QueryEmbedding     []float32
	Filters            map[string][]string
	SourceSeq          int64
	Limit              int
}

// RetrievalRepo 同时保存不可变 Profile、Snapshot 状态机和 pgvector Chunk。
// Snapshot 终态写入带 NodeRun attempt fencing，外部索引调用不进入数据库事务。
type RetrievalRepo interface {
	GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error)
	GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error)
	ListDatasetItemsAfter(ctx context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error)

	CreateRetrievalProfile(ctx context.Context, profile *model.RetrievalProfile) error
	GetRetrievalProfile(ctx context.Context, id string) (*model.RetrievalProfile, error)
	ListRetrievalProfiles(ctx context.Context, workspaceID, datasetSchemaID string, limit int) ([]model.RetrievalProfile, error)

	GetOrCreateRetrievalSnapshotForNode(ctx context.Context, snapshot *model.RetrievalSnapshot, producerAttempt int) (*model.RetrievalSnapshot, error)
	GetRetrievalSnapshot(ctx context.Context, id string) (*model.RetrievalSnapshot, error)
	ListRetrievalSnapshots(ctx context.Context, datasetID, profileID, status string, limit int) ([]model.RetrievalSnapshot, error)
	GetLatestActiveRetrievalSnapshot(ctx context.Context, datasetID, profileID string, throughSeq int64) (*model.RetrievalSnapshot, error)
	SetRetrievalSnapshotStatusForNode(ctx context.Context, snapshotID, nodeRunID string, producerAttempt int, status, failureReason string) error
	ActivateRetrievalSnapshotForNode(ctx context.Context, snapshotID, nodeRunID string, producerAttempt int,
		lexicalRef, vectorRef string, lexicalCount, vectorCount int) (*model.RetrievalSnapshot, error)

	UpsertRetrievalChunks(ctx context.Context, chunks []model.RetrievalChunk) error
	CountRetrievalChunks(ctx context.Context, datasetID, profileID string, sourceSeq int64) (chunkCount, itemCount int, err error)
	SearchRetrievalChunks(ctx context.Context, req VectorSearchRequest) ([]RankedHit, error)
	GetDatasetItemsByIDs(ctx context.Context, datasetID string, sourceSeq int64, ids []string) ([]model.DatasetItem, error)

	AppendKnowledgeToolAudit(ctx context.Context, audit KnowledgeToolAudit) error
}

// Reranker 与 embedding 共享供应商凭证，但保留独立端口和模型合同。
type Reranker interface {
	Available() bool
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error)
}

type RerankResult struct {
	Index int
	Score float64
}

type KnowledgeToolAudit struct {
	ID           string
	ScopeID      string
	WorkspaceID  string
	ToolName     string
	SourceName   string
	Request      json.RawMessage
	ResultCount  int
	LatencyMS    int64
	ErrorMessage string
}
