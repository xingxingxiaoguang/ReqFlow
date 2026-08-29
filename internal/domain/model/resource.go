package model

import (
	"encoding/json"
	"time"
)

type ResourceType string

const (
	ResourceAssetSet          ResourceType = "asset_set"
	ResourceParsedDocuments   ResourceType = "parsed_documents"
	ResourceRecordDrafts      ResourceType = "record_drafts"
	ResourceApprovedRecords   ResourceType = "approved_records"
	ResourceDataset           ResourceType = "dataset"
	ResourceDatasetBoundary   ResourceType = "dataset_boundary"
	ResourceDatasetBatch      ResourceType = "dataset_batch"
	ResourceRetrievalSnapshot ResourceType = "retrieval_snapshot"
	ResourceArtifact          ResourceType = "artifact"
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
