-- ReqFlow 初始结构：同步缓存 + 向量列 + 元数据 + 导入记录
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE projects (
    id                 TEXT PRIMARY KEY,          -- PingCode 项目 ID
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    remote_updated_at  TEXT,
    is_archived        BOOLEAN NOT NULL DEFAULT FALSE,
    embedding          vector(1024),
    synced_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE work_items (
    id                 TEXT PRIMARY KEY,          -- PingCode 工作项 ID
    project_id         TEXT NOT NULL,
    identifier         TEXT NOT NULL DEFAULT '',  -- 如 WI-123，bug 编号匹配用
    title              TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL DEFAULT '',  -- 类型 group：story/task/bug…
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

-- PingCode 元数据缓存（名称 → UUID 映射用；第二波 bug 关联匹配同样复用）
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
    type               TEXT NOT NULL DEFAULT '',  -- pending | doing | done
    color              TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, project_id, work_item_type_id)
);
CREATE TABLE work_item_priorities (
    id          TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    name        TEXT NOT NULL,
    PRIMARY KEY (id, project_id)
);
-- 自定义属性：第一波建表不拉取（扩展点）
CREATE TABLE work_item_properties (
    id                 TEXT NOT NULL,
    project_id         TEXT NOT NULL,
    work_item_type_id  TEXT NOT NULL,
    name               TEXT NOT NULL,
    type               TEXT NOT NULL DEFAULT '',
    options            JSONB,
    PRIMARY KEY (id, project_id, work_item_type_id)
);

-- 需求文档分析 → 导入记录
CREATE TABLE import_records (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    file_name            TEXT NOT NULL,
    original_file_path   TEXT,
    status               TEXT NOT NULL DEFAULT 'analyzed',  -- analyzed|importing|success|partial_success|failed
    items_count          INT  NOT NULL DEFAULT 0,
    target_project_id    TEXT,
    target_project_name  TEXT,
    imported_count       INT  NOT NULL DEFAULT 0,
    failed_count         INT  NOT NULL DEFAULT 0,
    error_message        TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE import_record_items (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    record_id             UUID NOT NULL REFERENCES import_records (id) ON DELETE CASCADE,
    title                 TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    project_name          TEXT NOT NULL DEFAULT '',
    type_id               TEXT NOT NULL DEFAULT '',
    priority              TEXT NOT NULL DEFAULT '',
    estimated_hours       DOUBLE PRECISION,
    start_at              TEXT,
    end_at                TEXT,
    assignee_name         TEXT,
    solution_suggestion   TEXT NOT NULL DEFAULT '',
    pingcode_id           TEXT,
    pingcode_identifier   TEXT,
    status                TEXT NOT NULL DEFAULT 'pending',  -- pending|success|failed
    error_message         TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_import_record_items_record ON import_record_items (record_id);
