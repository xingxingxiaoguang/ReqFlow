package model

import (
	"encoding/json"
	"time"
)

type LexicalConfig struct {
	Fields   map[string]float64 `json:"fields"`
	Analyzer string             `json:"analyzer"`
}

type VectorConfig struct {
	Fields         []string `json:"fields"`
	ChunkSize      int      `json:"chunk_size"`
	ChunkOverlap   int      `json:"chunk_overlap"`
	ChunkerVersion string   `json:"chunker_version"`
	EmbeddingModel string   `json:"embedding_model"`
}

type FusionConfig struct {
	Method            string `json:"method"`
	RankConstant      int    `json:"rank_constant"`
	LexicalCandidates int    `json:"lexical_candidates"`
	VectorCandidates  int    `json:"vector_candidates"`
}

// RetrievalProfile 是创建后不可修改的索引合同。
type RetrievalProfile struct {
	ID              string
	WorkspaceID     string
	Name            string
	DatasetSchemaID string
	Lexical         LexicalConfig
	Vector          VectorConfig
	FilterFields    []string
	Fusion          FusionConfig
	ProfileHash     string
	CreatedAt       time.Time
}

type RetrievalSnapshot struct {
	ID                 string
	DatasetID          string
	RetrievalProfileID string
	ProducerNodeRunID  string
	SourceSeq          int64
	Status             string
	LexicalRef         string
	VectorRef          string
	LexicalCount       int
	VectorCount        int
	FailureReason      string
	CreatedAt          time.Time
	ActivatedAt        time.Time
}

// RetrievalSearchMode 控制单次查询启用哪些召回通道。索引合同由
// RetrievalProfile 固化，业务侧的权重、阈值和数量必须放在查询请求中。
type RetrievalSearchMode string

const (
	RetrievalModeLexical  RetrievalSearchMode = "lexical"
	RetrievalModeSemantic RetrievalSearchMode = "semantic"
	RetrievalModeHybrid   RetrievalSearchMode = "hybrid"
)

// RetrievalSearchStrategy 是运行时检索策略，不属于不可变 Profile。
// ScoreThreshold 使用 0..1 的归一化融合分数；RerankTopN 仅在启用重排序时生效。
type RetrievalSearchStrategy struct {
	Mode           RetrievalSearchMode `json:"mode"`
	LexicalWeight  float64             `json:"lexical_weight"`
	SemanticWeight float64             `json:"semantic_weight"`
	ScoreThreshold float64             `json:"score_threshold"`
	RecallLimit    int                 `json:"recall_limit"`
	TopK           int                 `json:"top_k"`
	RerankEnabled  bool                `json:"rerank_enabled"`
	RerankTopN     int                 `json:"rerank_top_n"`
}

type RetrievalRankSource struct {
	Rank  int     `json:"rank"`
	Score float64 `json:"score"`
}

// RetrievalHit 保留融合前后的排名证据。Score 是最终排序分数：未重排时等于
// FusionScore，启用重排时等于 RerankScore。
type RetrievalHit struct {
	DatasetItemID string                         `json:"dataset_item_id"`
	ChunkID       string                         `json:"chunk_id,omitempty"`
	ChunkText     string                         `json:"chunk_text,omitempty"`
	Fields        json.RawMessage                `json:"fields"`
	Provenance    json.RawMessage                `json:"provenance"`
	CommitSeq     int64                          `json:"commit_seq"`
	Score         float64                        `json:"score"`
	FusionScore   float64                        `json:"fusion_score"`
	RerankScore   *float64                       `json:"rerank_score,omitempty"`
	Ranks         map[string]RetrievalRankSource `json:"ranks"`
}

const (
	RetrievalSnapshotBuilding   = "building"
	RetrievalSnapshotValidating = "validating"
	RetrievalSnapshotActive     = "active"
	RetrievalSnapshotFailed     = "failed"
	RetrievalSnapshotRetired    = "retired"
)

type RetrievalChunk struct {
	ID                 string
	DatasetID          string
	DatasetItemID      string
	RetrievalProfileID string
	ChunkNo            int
	ChunkText          string
	ChunkHash          string
	SourceSeq          int64
	EmbeddingModel     string
	Embedding          []float32
	Metadata           json.RawMessage
	CreatedAt          time.Time
}
