-- 0005: 数据集管理 —— 剪除 PingCode 依赖，任务间通过数据集衔接（闭环）。
-- 需求导入产出需求数据集，Bug 分析等后续任务以需求数据集为输入。
-- 向量语料从平台同步表迁移到数据集条目（查重/关联匹配/agent 工具的底料）。

CREATE TABLE datasets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL,                -- requirement | bug | …
    name            TEXT NOT NULL,
    source_task_id  UUID,                         -- 产生该数据集的任务（可选，人工导入的数据集为空）
    status          TEXT NOT NULL DEFAULT 'ready', -- ready | building（写入中，未发布）
    item_count      INT  NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_datasets_type ON datasets (type, created_at DESC);

CREATE TABLE dataset_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id  UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    fields      TEXT NOT NULL,                    -- 类型化字段 JSON 文本（需求=草稿形状 title/description/priority/…）
    embedding   vector(1024),                     -- 语义检索（查重/关联匹配）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dataset_items_dataset ON dataset_items (dataset_id);
CREATE INDEX idx_dataset_items_hnsw ON dataset_items USING hnsw (embedding vector_cosine_ops);

-- 任务产出/消费的数据集（任务间衔接）
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS output_dataset_id TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS input_dataset_id TEXT;

-- task_items 剪除平台回写列（草稿缓冲不再需要）
ALTER TABLE task_items DROP COLUMN IF EXISTS pingcode_id;
ALTER TABLE task_items DROP COLUMN IF EXISTS pingcode_identifier;

-- 剪除平台同步语料表（projects/work_items 及其元数据）
DROP TABLE IF EXISTS work_item_properties;
DROP TABLE IF EXISTS work_item_states;
DROP TABLE IF EXISTS work_item_priorities;
DROP TABLE IF EXISTS work_item_types;
DROP TABLE IF EXISTS work_items;
DROP TABLE IF EXISTS projects;
