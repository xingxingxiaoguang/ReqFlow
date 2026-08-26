-- 0003: 任务化改造 —— 长程流程（需求导入/同步/bug）统一为 task 生命周期管理。
-- 研发阶段无历史数据：直接建新表并移除旧的导入记录表（导入记录并入任务管理）。
-- input/output/data 用 TEXT（JSON 文本）而非 JSONB：GORM 字符串参数直写无需类型转换，
-- 与 0002 的 agent_context 决策一致；当前无库内查询需求。

CREATE TABLE tasks (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type                 TEXT NOT NULL,                      -- requirement_import | sync | bug_*（第二波）
    title                TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending',    -- pending|running|awaiting|paused|succeeded|failed
    current_step         INT  NOT NULL DEFAULT 0,            -- 当前步骤序号（0=未开始）
    input                TEXT,                               -- JSON 文本：文件信息/解析文本/附加要求（空=nil）
    output               TEXT,                               -- JSON 文本：导入统计/新建项目等
    agent_context        TEXT,                               -- 分析会话 JSON（port.Context，暂停续跑载体）
    items_count          INT  NOT NULL DEFAULT 0,
    imported_count       INT  NOT NULL DEFAULT 0,
    failed_count         INT  NOT NULL DEFAULT 0,
    target_project_id    TEXT,
    target_project_name  TEXT,
    error_message        TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at           TIMESTAMPTZ,
    finished_at          TIMESTAMPTZ
);
CREATE INDEX idx_tasks_status ON tasks (status, created_at DESC);

CREATE TABLE task_steps (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    seq         INT  NOT NULL,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- pending|running|succeeded|failed|awaiting
    detail      TEXT NOT NULL DEFAULT '',
    data        TEXT,                            -- JSON 文本：工具轨迹/导入汇总等（空=nil）
    started_at  TIMESTAMPTZ,
    ended_at    TIMESTAMPTZ
);
CREATE INDEX idx_task_steps_task ON task_steps (task_id, seq);

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
    status                TEXT NOT NULL DEFAULT 'pending', -- pending|success|failed
    error_message         TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_items_task ON task_items (task_id);

-- 导入记录并入任务管理
DROP TABLE IF EXISTS import_record_items;
DROP TABLE IF EXISTS import_records;
