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
