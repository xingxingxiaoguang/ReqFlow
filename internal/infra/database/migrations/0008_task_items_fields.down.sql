-- 回退到 0003 的 requirement 定型列形状（仅供 revert；研发阶段无正式数据）。
DROP TABLE task_items;

CREATE TABLE task_items (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id               UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    title                 TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    project_name          TEXT NOT NULL DEFAULT '',
    type_id               TEXT NOT NULL DEFAULT '',
    priority              TEXT NOT NULL DEFAULT '',
    estimated_hours       DOUBLE PRECISION,
    start_at              TEXT,
    end_at                TEXT,
    assignee_name         TEXT,
    state                 TEXT NOT NULL DEFAULT '',
    solution_suggestion   TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'pending',
    error_message         TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_items_task ON task_items (task_id);
