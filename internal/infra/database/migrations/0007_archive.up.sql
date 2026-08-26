-- 0007: 归档 —— 任务与数据集的删除进入独立归档表，可查可恢复；
-- 已归档数据物理上离开主表，主业务循环（列表/查重语料/语义检索/统计）自动不再触达。
-- 归档表与主表同构（LIKE 直搬，不带索引与外键——冷数据不占检索成本）；
-- 任务步骤/明细量小，以 JSON 快照内嵌于归档任务行；数据集条目（含向量）结构化直搬。

CREATE TABLE archived_tasks (
    LIKE tasks INCLUDING DEFAULTS
);
ALTER TABLE archived_tasks
    ADD COLUMN steps_snapshot TEXT NOT NULL DEFAULT '[]',  -- TaskStep[] JSON 快照
    ADD COLUMN items_snapshot TEXT NOT NULL DEFAULT '[]',  -- TaskItem[] JSON 快照
    ADD COLUMN archived_at    TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX idx_archived_tasks_time ON archived_tasks (archived_at DESC);
CREATE INDEX idx_archived_tasks_type ON archived_tasks (type);

CREATE TABLE archived_datasets (
    LIKE datasets INCLUDING DEFAULTS
);
ALTER TABLE archived_datasets
    ADD COLUMN archived_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX idx_archived_datasets_time ON archived_datasets (archived_at DESC);

CREATE TABLE archived_dataset_items (
    LIKE dataset_items INCLUDING DEFAULTS
);
ALTER TABLE archived_dataset_items
    ADD COLUMN archived_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX idx_archived_dataset_items_ds ON archived_dataset_items (dataset_id);
