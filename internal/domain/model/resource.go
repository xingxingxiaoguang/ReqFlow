package model

import (
	"encoding/json"
	"time"
)

type ResourceType string

const (
	ResourceAssetSet           ResourceType = "asset_set"
	ResourceParsedDocuments    ResourceType = "parsed_documents"
	ResourceRecordDrafts       ResourceType = "record_drafts"
	ResourceTransformedRecords ResourceType = "transformed_records"
	ResourceValidationResults  ResourceType = "validation_results"
	ResourceApprovedRecords    ResourceType = "approved_records"
	ResourceDataset            ResourceType = "dataset"
	ResourceDatasetBoundary    ResourceType = "dataset_boundary"
	ResourceDatasetBatch       ResourceType = "dataset_batch"
	ResourcePipelineCursor     ResourceType = "pipeline_cursor"
	ResourceRetrievalSnapshot  ResourceType = "retrieval_snapshot"
	ResourceArtifact           ResourceType = "artifact"
)

type ResourceDirection string

const (
	ResourceInput  ResourceDirection = "input"
	ResourceOutput ResourceDirection = "output"
)

// PortDefinition 声明任务端口接受的资源类型。
type PortDefinition struct {
	ResourceType ResourceType `json:"resource_type"`
	Required     bool         `json:"required,omitempty"`
	Description  string       `json:"description,omitempty"`
}

// TaskResourceBinding 将一次任务的逻辑端口固化到具体资源及其读取边界。
type TaskResourceBinding struct {
	ID           string
	TaskID       string
	PortName     string
	Direction    ResourceDirection
	ResourceType ResourceType
	ResourceID   string
	Boundary     json.RawMessage
	CreatedAt    time.Time
}

// ResourceRef 是 Executor 可消费或产出的稳定资源引用。Boundary 固化读取边界，
// 例如 Dataset 的 through_seq；资源内容本身由对应应用服务/仓储读取。
type ResourceRef struct {
	ResourceType ResourceType    `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Boundary     json.RawMessage `json:"boundary,omitempty"`
}

// StepResourceBinding 保存一次 StepRun 的具名输出。下游步骤只通过它解析
// $step.<step_id>.<port>，不从 progress/checkpoint 猜测业务资源。
type StepResourceBinding struct {
	ID           string
	StepRunID    string
	PortName     string
	ResourceType ResourceType
	ResourceID   string
	Boundary     json.RawMessage
	CreatedAt    time.Time
}

// DatasetBoundary 固化 Task 对追加型 Dataset 的读取上界。
type DatasetBoundary struct {
	DatasetID  string `json:"dataset_id"`
	ThroughSeq int64  `json:"through_seq"`
}

// RetrievalBoundary 固化 Task 使用的检索快照。
type RetrievalBoundary struct {
	RetrievalSnapshotID string `json:"retrieval_snapshot_id"`
	SourceSeq           int64  `json:"source_seq"`
}

// ParsedDocumentsBoundary 描述 source.parse 输出 Manifest 的解析身份与原始集合。
// Manifest 本身终态不可变，Boundary 主要用于审计和输入哈希的自描述性。
type ParsedDocumentsBoundary struct {
	AssetSetID    string `json:"asset_set_id"`
	ParserName    string `json:"parser_name"`
	ParserVersion string `json:"parser_version"`
}

// RecordDraftsBoundary 固化 llm.extract 的输入与抽取合同身份。草稿 Manifest 自身
// 终态不可变；这些字段让下游输入哈希和审计记录无需反查可变配置。
type RecordDraftsBoundary struct {
	ParsedDocumentSetID string `json:"parsed_document_set_id"`
	ExtractionProfileID string `json:"extraction_profile_id"`
	TargetSchemaID      string `json:"target_schema_id"`
	ProfileHash         string `json:"profile_hash"`
	Model               string `json:"model"`
}

// TransformedRecordsBoundary 固化确定性转换所使用的草稿、Profile、Schema 与引擎版本。
// 同一 StepRun 恢复时若引擎版本变化，必须拒绝混合新旧转换结果。
type TransformedRecordsBoundary struct {
	RecordDraftSetID       string `json:"record_draft_set_id"`
	ExtractionProfileID    string `json:"extraction_profile_id"`
	TargetSchemaID         string `json:"target_schema_id"`
	ProfileHash            string `json:"profile_hash"`
	TransformEngineVersion string `json:"transform_engine_version"`
}

// ValidationResultsBoundary 固化校验所针对的目标 Dataset 及其读取上界。后续发布会
// 再次对当前 Dataset 做冲突校验，但审核看到的结果始终对应这里的 through_seq 快照。
type ValidationResultsBoundary struct {
	TransformedRecordSetID  string `json:"transformed_record_set_id"`
	TargetDatasetID         string `json:"target_dataset_id"`
	TargetSchemaID          string `json:"target_schema_id"`
	ValidatedThroughSeq     int64  `json:"validated_through_seq"`
	ValidationEngineVersion string `json:"validation_engine_version"`
}

// ApprovedRecordsBoundary 固化人工审核所依据的校验快照和审核时看到的目标
// Dataset 上界。ApprovedRecordSet 自身不可变；发布时仍需在当前 Dataset 上做
// 最终唯一性校验，防止审核完成后的并发追加绕过冲突保护。
type ApprovedRecordsBoundary struct {
	ValidationResultSetID string `json:"validation_result_set_id"`
	TargetDatasetID       string `json:"target_dataset_id"`
	TargetSchemaID        string `json:"target_schema_id"`
	ReviewedThroughSeq    int64  `json:"reviewed_through_seq"`
	ReviewHash            string `json:"review_hash"`
}

// DatasetBatchBoundary 描述一次已经原子提交的追加范围。
type DatasetBatchBoundary struct {
	DatasetID string `json:"dataset_id"`
	FromSeq   int64  `json:"from_seq"`
	ToSeq     int64  `json:"to_seq"`
}

// PipelineCursorBoundary 固化一次派生任务成功消费的源位点及对应目标 Batch。
// Cursor 行本身会继续前移，因此消费者必须依赖 Boundary，而不是反查当前值。
type PipelineCursorBoundary struct {
	PipelineKey         string `json:"pipeline_key"`
	SourceDatasetID     string `json:"source_dataset_id"`
	TargetDatasetID     string `json:"target_dataset_id"`
	ProcessedThroughSeq int64  `json:"processed_through_seq"`
	TargetBatchID       string `json:"target_batch_id"`
	TargetThroughSeq    int64  `json:"target_through_seq"`
}
