-- 0011 回退：恢复旧四张表结构（TEXT 字段袋 + schema_version 钉版；无数据）。
-- 研发阶段推倒重建约定的对称回退，仅保证迁移器可逆，不承诺数据保留。

DROP TABLE IF EXISTS archived_dataset_items;
DROP TABLE IF EXISTS archived_datasets;
DROP TABLE IF EXISTS dataset_items;
DROP TABLE IF EXISTS datasets;

CREATE TABLE datasets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type            TEXT NOT NULL,
    name            TEXT NOT NULL,
    source_task_id  UUID,
    status          TEXT NOT NULL DEFAULT 'ready',
    item_count      INT  NOT NULL DEFAULT 0,
    description     TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '[]',
    schema_version  INT  NOT NULL DEFAULT 1,
    extra           TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_datasets_type ON datasets (type, created_at DESC);

CREATE TABLE dataset_items (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    fields         TEXT NOT NULL,
    item_key       TEXT NOT NULL DEFAULT '',
    fingerprint    TEXT NOT NULL DEFAULT '',
    metadata       TEXT NOT NULL DEFAULT '{}',
    source_task_id UUID,
    embedding      vector(1024),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dataset_items_dataset ON dataset_items (dataset_id);
CREATE UNIQUE INDEX uq_dataset_items_key
    ON dataset_items (dataset_id, item_key) WHERE item_key <> '';
CREATE INDEX idx_dataset_items_hnsw ON dataset_items USING hnsw (embedding vector_cosine_ops);

ALTER TABLE task_items ALTER COLUMN fields TYPE TEXT USING fields::text;

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
