-- 0005 回滚：重建平台同步表（空表，研发阶段无数据）
ALTER TABLE tasks DROP COLUMN IF EXISTS output_dataset_id;
ALTER TABLE tasks DROP COLUMN IF EXISTS input_dataset_id;
DROP TABLE IF EXISTS dataset_items;
DROP TABLE IF EXISTS datasets;

CREATE TABLE projects (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    remote_updated_at  TEXT,
    is_archived        BOOLEAN NOT NULL DEFAULT FALSE,
    embedding          vector(1024),
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE work_items (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL,
    identifier         TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL DEFAULT '',
    type_id            TEXT NOT NULL DEFAULT '',
    state_id           TEXT NOT NULL DEFAULT '',
    remote_updated_at  TEXT,
    is_archived        BOOLEAN NOT NULL DEFAULT FALSE,
    embedding          vector(1024),
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_work_items_project  ON work_items (project_id) WHERE NOT is_archived;
CREATE INDEX idx_work_items_hnsw     ON work_items USING hnsw (embedding vector_cosine_ops) WHERE NOT is_archived;
CREATE INDEX idx_projects_hnsw       ON projects   USING hnsw (embedding vector_cosine_ops) WHERE NOT is_archived;
CREATE TABLE work_item_types (
    id          TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    "group"     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, project_id)
);
CREATE TABLE work_item_states (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    work_item_type_id  TEXT NOT NULL,
    name               TEXT NOT NULL,
    type               TEXT NOT NULL DEFAULT '',
    color              TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, project_id, work_item_type_id)
);
CREATE TABLE work_item_priorities (
    id          TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    PRIMARY KEY (id, project_id)
);
CREATE TABLE work_item_properties (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    work_item_type_id  TEXT NOT NULL,
    name               TEXT NOT NULL,
    type               TEXT NOT NULL DEFAULT '',
    options            JSONB,
    PRIMARY KEY (id, project_id, work_item_type_id)
);
