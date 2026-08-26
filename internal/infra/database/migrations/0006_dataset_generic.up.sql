-- 0006: 通用数据集地基 —— 条目身份（item_key/fingerprint）+ 数据集元数据 + 写入策略支撑。
-- item_key: schema KeyFields 归一化拼接（条目业务主键，upsert/去重判定）
-- fingerprint: 全字段内容哈希（变更检测；相同则跳过重嵌与更新）
-- 部分唯一索引：存量条目（key 为空，剪除 PingCode 前的旧数据）不参与唯一约束，
-- 新写入条目一律携带 key。

ALTER TABLE dataset_items
    ADD COLUMN IF NOT EXISTS item_key       TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS fingerprint    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata       TEXT NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS source_task_id UUID,
    ADD COLUMN IF NOT EXISTS updated_at     TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE UNIQUE INDEX IF NOT EXISTS uq_dataset_items_key
    ON dataset_items (dataset_id, item_key) WHERE item_key <> '';

ALTER TABLE datasets
    ADD COLUMN IF NOT EXISTS description    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tags           TEXT NOT NULL DEFAULT '[]',
    ADD COLUMN IF NOT EXISTS schema_version INT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS extra          TEXT NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS updated_at     TIMESTAMPTZ NOT NULL DEFAULT now();
