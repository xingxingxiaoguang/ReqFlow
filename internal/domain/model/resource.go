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
