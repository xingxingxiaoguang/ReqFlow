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
	ValidationResultID  string            `json:"validation_result_id,omitempty"`
	ApprovedRecordSetID string            `json:"approved_record_set_id,omitempty"`
	ReviewDecisionID    string            `json:"review_decision_id,omitempty"`
	ReviewAction        string            `json:"review_action,omitempty"`
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

// RecordIssue 是转换、Schema 校验和业务规则校验共享的稳定问题形状。
// Code 面向程序和测试，Message 面向审核界面；Severity 只允许 warning/error。
type RecordIssue struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

const (
	RecordIssueWarning = "warning"
	RecordIssueError   = "error"
)

// RecordChange 保存确定性转换前后的字段值，用于人工审核展示修改 Diff。
type RecordChange struct {
	Field     string          `json:"field"`
	Operation string          `json:"operation"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
}

// TransformedRecordSet 是 data.transform 的不可变输出 Manifest。存在于表中的
// TransformedRecord 都已经确定性处理完成；恢复时仅补齐尚未生成的记录。
type TransformedRecordSet struct {
	ID                  string
	RecordDraftSetID    string
	ExtractionProfileID string
	TargetSchemaID      string
	SourceStepRunID     string
	Status              string
	ProducerAttempt     int
	EngineVersion       string
	DraftCount          int
	TransformedCount    int
	ChangedRecordCount  int
	IssueCount          int
	CreatedAt           time.Time
	FinishedAt          time.Time
}

const (
	TransformedRecordSetRunning   = "running"
	TransformedRecordSetSucceeded = "succeeded"
)

type TransformedRecord struct {
	ID                     string
	TransformedRecordSetID string
	RecordDraftID          string
	Ordinal                int
	Fields                 json.RawMessage
	Changes                []RecordChange
	Issues                 []RecordIssue
	CreatedAt              time.Time
}

// ValidationResultSet 是 data.validate 针对目标 Dataset 某一 commit_seq 上界的
// 不可变校验快照。单条 invalid/duplicate/conflict 是业务结果，不会让 Manifest 失败。
type ValidationResultSet struct {
	ID                     string
	TransformedRecordSetID string
	TargetDatasetID        string
	TargetSchemaID         string
	SourceStepRunID        string
	Status                 string
	ProducerAttempt        int
	EngineVersion          string
	ValidatedThroughSeq    int64
	RecordCount            int
	ValidCount             int
	WarningCount           int
	InvalidCount           int
	DuplicateCount         int
	ConflictCount          int
	CreatedAt              time.Time
	FinishedAt             time.Time
}

const (
	ValidationResultSetRunning   = "running"
	ValidationResultSetSucceeded = "succeeded"

	ValidationRecordValid     = "valid"
	ValidationRecordWarning   = "warning"
	ValidationRecordInvalid   = "invalid"
	ValidationRecordDuplicate = "duplicate_in_batch"
	ValidationRecordConflict  = "conflict_existing_key"
)

type ValidationResult struct {
	ID                    string
	ValidationResultSetID string
	TransformedRecordID   string
	Ordinal               int
	Fields                json.RawMessage
	ItemKey               string
	Fingerprint           string
	Status                string
	Issues                []RecordIssue
	CreatedAt             time.Time
}

// ApprovedRecordSet 是一次人工 Gate 的不可变审核结论。审核必须覆盖来源
// ValidationResultSet 的全部记录，避免前端漏传导致数据被静默丢弃。
type ApprovedRecordSet struct {
	ID                    string
	ValidationResultSetID string
	TargetDatasetID       string
	TargetSchemaID        string
	SourceStepRunID       string
	Reviewer              string
	Rationale             string
	ReviewHash            string
	ReviewedThroughSeq    int64
	RecordCount           int
	ApprovedCount         int
	EditedCount           int
	ExcludedCount         int
	CreatedAt             time.Time
}

const (
	ReviewActionApprove = "approve"
	ReviewActionEdit    = "edit"
	ReviewActionExclude = "exclude"
)

// RecordReviewDecision 保存每条校验结果的人工决定。Fields 对 approve/edit 是可发布
// 的最终字段快照；exclude 仍保存审核时字段，便于独立审计而无需猜测前端状态。
type RecordReviewDecision struct {
	ID                  string
	ApprovedRecordSetID string
	ValidationResultID  string
	TransformedRecordID string
	Ordinal             int
	Action              string
	Fields              json.RawMessage
	ItemKey             string
	Fingerprint         string
	Issues              []RecordIssue
	Provenance          ItemProvenance
	Note                string
	CreatedAt           time.Time
}
