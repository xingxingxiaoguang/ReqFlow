-- 0003 回滚：移除任务表，重建旧导入记录表（研发阶段无数据，重建即空表）
DROP TABLE IF EXISTS task_items;
DROP TABLE IF EXISTS task_steps;
DROP TABLE IF EXISTS tasks;

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
    agent_context        TEXT,
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
