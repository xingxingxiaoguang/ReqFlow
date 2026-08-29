package model

import (
	"encoding/json"
	"time"
)

// DatasetSchemaDefinition 是创建后不可修改的数据结构合同。
// 抽取提示和检索配置不得写入 JSONSchema。
type DatasetSchemaDefinition struct {
	ID          string
	WorkspaceID string
	Name        string
	Description string
	JSONSchema  json.RawMessage
	UISchema    json.RawMessage
	SchemaHash  string
	CreatedAt   time.Time
}

// DatasetPurpose 只用于目录与产品展示，不参与字段校验。
type DatasetPurpose string

const (
	DatasetPurposeBase      DatasetPurpose = "base"
	DatasetPurposeQuery     DatasetPurpose = "query"
	DatasetPurposeAnalysis  DatasetPurpose = "analysis"
	DatasetPurposeGraphNode DatasetPurpose = "graph_node"
	DatasetPurposeGraphEdge DatasetPurpose = "graph_edge"
)

const (
	DatasetStatusActive   = "active"
	DatasetStatusSealed   = "sealed"
	DatasetStatusArchived = "archived"
)

// DatasetBatch 是一次任务对 Dataset 的原子追加单元。
type DatasetBatch struct {
	ID              string
	DatasetID       string
	SourceTaskID    string
	SourceStepRunID string
	Status          string
	ItemCount       int
	FromSeq         int64
	ToSeq           int64
	PayloadHash     string
	ErrorMessage    string
	CreatedAt       time.Time
	CommittedAt     time.Time
}

const (
	DatasetBatchStaging    = "staging"
	DatasetBatchValidating = "validating"
	DatasetBatchCommitted  = "committed"
	DatasetBatchRejected   = "rejected"
)

// DatasetAlias 为不可变 Dataset 提供稳定的业务入口；任务创建时必须解析到具体 DatasetID。
type DatasetAlias struct {
	ID              string
	WorkspaceID     string
	Name            string
	ActiveDatasetID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SourceReference 将结构化条目追溯到基础条目或原文区块。
type SourceReference struct {
	DatasetItemID string `json:"dataset_item_id,omitempty"`
	AssetID       string `json:"asset_id,omitempty"`
	BlockID       string `json:"block_id,omitempty"`
	PageNo        int    `json:"page_no,omitempty"`
	Quote         string `json:"quote,omitempty"`
}

// ItemProvenance 记录产生 DatasetItem 的可审计上下文。
type ItemProvenance struct {
	SourceRefs          []SourceReference `json:"source_refs,omitempty"`
	ExtractionProfileID string            `json:"extraction_profile_id,omitempty"`
	Model               string            `json:"model,omitempty"`
	PromptHash          string            `json:"prompt_hash,omitempty"`
	QualityStatus       string            `json:"quality_status,omitempty"`
}

// PipelineCursor 只在目标 Batch 成功提交后推进，用于增量数据处理。
type PipelineCursor struct {
	ID                  string
	PipelineKey         string
	SourceDatasetID     string
	TargetDatasetID     string
	ProcessedThroughSeq int64
	LastSuccessTaskID   string
	UpdatedAt           time.Time
}

// ExtractionProfile 是创建后不可修改的抽取、归一化和校验配置。
type ExtractionProfile struct {
	ID                 string
	WorkspaceID        string
	Name               string
	TargetSchemaID     string
	RecordGranularity  string
	SystemInstruction  string
	FieldGuides        json.RawMessage
	Examples           json.RawMessage
	NormalizationRules json.RawMessage
	ValidationRules    json.RawMessage
	ProfileHash        string
	CreatedAt          time.Time
}

// RecordDraftSet 是 llm.extract 的一等输出 Manifest。它与一个解析结果集和一个
// 不可变 ExtractionProfile 绑定，后续 transform/validate 只读取这个快照资源。
type RecordDraftSet struct {
	ID                  string
	ParsedDocumentSetID string
	ExtractionProfileID string
	SourceStepRunID     string
	Status              string
	ProducerAttempt     int
	Model               string
	UnitCount           int
	SucceededUnitCount  int
	FailedUnitCount     int
	DraftCount          int
	LLMRequestCount     int
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheWriteTokens    int
	CreatedAt           time.Time
	FinishedAt          time.Time
}

const (
	RecordDraftSetRunning   = "running"
	RecordDraftSetSucceeded = "succeeded"
	RecordDraftSetPartial   = "partial"
	RecordDraftSetFailed    = "failed"
)

// ExtractionUnit 是按 DocumentBlock 稳定切分的最小 LLM 调用单元。UnitKey 和
// InputHash 由输入区块与 Profile 决定，重试时成功单元不会重复调用模型。
type ExtractionUnit struct {
	ID                string
	RecordDraftSetID  string
	UnitKey           string
	ParsedDocumentID  string
	Ordinal           int
	FirstBlockOrdinal int
	LastBlockOrdinal  int
	InputHash         string
	Status            string
	ErrorMessage      string
	ResponseHash      string
	RequestCount      int
	InputTokens       int
	OutputTokens      int
	CacheReadTokens   int
	CacheWriteTokens  int
	CreatedAt         time.Time
	FinishedAt        time.Time
}

// LLMUsage 是领域层可持久化的模型用量快照；Provider 的响应类型在应用层转换，
// 仓储不依赖具体 LLM port 实现。
type LLMUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

const (
	ExtractionUnitPending   = "pending"
	ExtractionUnitRunning   = "running"
	ExtractionUnitSucceeded = "succeeded"
	ExtractionUnitFailed    = "failed"
)

// RecordDraft 保留模型原始字段袋、逐字段置信度与可验证来源。这里不做最终类型
// 编码；确定性转换和业务校验由后续 Executor 完成。
type RecordDraft struct {
	ID               string
	RecordDraftSetID string
	ExtractionUnitID string
	Ordinal          int
	Fields           json.RawMessage
	FieldConfidence  json.RawMessage
	Provenance       ItemProvenance
	CreatedAt        time.Time
}
