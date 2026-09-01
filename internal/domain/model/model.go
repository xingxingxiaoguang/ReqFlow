// Package model 定义数据能力复用层的领域实体与值。
// 工作流设计与运行模型位于 internal/domain/workflow；本包不承载编排概念。
package model

import "time"

/* ---- Dataset 与条目（不可变 Schema + 只追加 Batch） ---- */

// Dataset 长期数据容器：Schema 与 key_fields 创建后固定，数据经不可变
// DatasetBatch 追加；CurrentSeq 是最近一次 Batch 原子提交的位点。
type Dataset struct {
	ID          string
	WorkspaceID string
	Purpose     DatasetPurpose // base / query / analysis / graph_node / graph_edge（目录分类）
	Name        string
	Description string
	SchemaID    string // 不可变 DatasetSchemaDefinition；结构变化 = 新 Schema + 新 Dataset
	KeyFields   []string
	Status      string // active | sealed | archived
	ItemCount   int
	CurrentSeq  int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DatasetItem 已提交条目：不 UPDATE/DELETE；身份 = ItemKey（key_fields 归一化拼接），
// Fingerprint 为规范化 fields 的稳定哈希；Provenance 保留 Batch/Task/Step/Asset/Block 链路。
type DatasetItem struct {
	ID          string
	DatasetID   string
	BatchID     string
	Fields      string // JSON 文本（受 Schema 校验的字段袋）
	ItemKey     string
	Fingerprint string
	CommitSeq   int64 // Dataset 内连续递增，仅 Batch 原子提交时分配
	Provenance  string
	CreatedAt   time.Time
}
