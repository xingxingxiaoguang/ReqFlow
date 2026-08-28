-- 0011: 字段定义归属数据集（推倒重建）—— datasets.schema 为字段定义的唯一真相源：
-- 任务绑定数据集后，分析提示词/写入校验/门内表格/查询全部按数据集自身的 schema 解析，
-- 类型级定义（metadata_registry kind=dataset_schema）降级为「数据集类型模板」。
-- 条目字段袋升级为原生 JSONB：表达式索引（FTS/筛选）可直接作用于 fields->>'key'，
-- 免去运行时 ::jsonb cast。研发阶段无历史数据，四张表整体重建（HANDOVER §6 迁移约定）。

DROP TABLE IF EXISTS archived_dataset_items;
DROP TABLE IF EXISTS archived_datasets;
DROP TABLE IF EXISTS dataset_items;
DROP TABLE IF EXISTS datasets;

CREATE TABLE datasets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL,                 -- 模板类型标识（新建时从模板带出 schema）
    name            TEXT NOT NULL,
    schema          JSONB NOT NULL,                -- 本数据集的字段定义（DatasetSchema JSON，创建时固化，实例受控演进）
    description     TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '[]',
    source_task_id  UUID,                          -- 产生该数据集的任务（可选，人工创建的数据集为空）
    status          TEXT NOT NULL DEFAULT 'ready', -- ready | building（写入中，未发布）
    item_count      INT  NOT NULL DEFAULT 0,
    schema_version  INT  NOT NULL DEFAULT 1,       -- 实例 schema 编辑计数（受控编辑递增）
    extra           TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_datasets_type ON datasets (type, created_at DESC);

CREATE TABLE dataset_items (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    fields         JSONB NOT NULL,                 -- 字段袋（本数据集 schema 类型化字段，原生 JSONB）
    item_key       TEXT NOT NULL DEFAULT '',       -- schema KeyFields 归一化拼接（upsert/去重基准）
    fingerprint    TEXT NOT NULL DEFAULT '',       -- 内容哈希（相同则跳过更新与重嵌）
    metadata       TEXT NOT NULL DEFAULT '{}',
    source_task_id UUID,
    embedding      vector(1024),                   -- 语义检索（查重/关联匹配）
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dataset_items_dataset ON dataset_items (dataset_id);
CREATE UNIQUE INDEX uq_dataset_items_key
    ON dataset_items (dataset_id, item_key) WHERE item_key <> '';
CREATE INDEX idx_dataset_items_hnsw ON dataset_items USING hnsw (embedding vector_cosine_ops);

-- 草稿字段袋与数据集条目同构，同步 JSONB（归一化/校验产物按数据集 schema 落 draft）
ALTER TABLE task_items ALTER COLUMN fields TYPE JSONB USING fields::jsonb;

-- 归档表与主表同构重建（LIKE 直搬，不带索引与外键——冷数据不占检索成本）
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
