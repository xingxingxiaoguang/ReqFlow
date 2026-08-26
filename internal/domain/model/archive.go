// 归档实体：任务与数据集的删除去处（可查可恢复，不参与主业务循环）。
package model

import "time"

// ArchivedTask 归档任务（含步骤/明细快照；恢复后可继续未走完的流程）。
type ArchivedTask struct {
	Task
	ArchivedAt time.Time
}

// ArchivedDataset 归档数据集（条目随行存在于 archived_dataset_items）。
type ArchivedDataset struct {
	Dataset
	ArchivedAt time.Time
}

// 归档对象种类（HTTP 路由用）。
const (
	ArchiveKindTask    = "task"
	ArchiveKindDataset = "dataset"
)
