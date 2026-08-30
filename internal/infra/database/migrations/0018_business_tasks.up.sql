-- 0018: Stage F 通用 Agent 分析结果与正式 Artifact fencing。

CREATE TABLE analysis_profiles (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  TEXT NOT NULL DEFAULT 'default',
    name          TEXT NOT NULL,
    instruction   TEXT NOT NULL,
    output_schema JSONB NOT NULL,
    profile_hash  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analysis_profiles_workspace
    ON analysis_profiles (workspace_id, created_at DESC);

CREATE TABLE analysis_results (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        TEXT NOT NULL DEFAULT 'default',
    analysis_profile_id UUID NOT NULL REFERENCES analysis_profiles (id),
    source_task_id      UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    source_step_run_id  UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    producer_attempt    INT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    output              JSONB NOT NULL DEFAULT '{}'::jsonb,
    agent_context       JSONB NOT NULL DEFAULT '{}'::jsonb,
    model               TEXT NOT NULL DEFAULT '',
    input_tokens        INT NOT NULL DEFAULT 0,
    output_tokens       INT NOT NULL DEFAULT 0,
    cache_read_tokens   INT NOT NULL DEFAULT 0,
    cache_write_tokens  INT NOT NULL DEFAULT 0,
    error_message       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at         TIMESTAMPTZ,
    UNIQUE (source_step_run_id)
);
CREATE INDEX idx_analysis_results_task
    ON analysis_results (source_task_id, created_at DESC);

ALTER TABLE artifacts
    ADD COLUMN producer_attempt INT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX uq_artifacts_source_step
    ON artifacts (source_step_run_id)
    WHERE source_step_run_id IS NOT NULL;

ALTER TABLE tasks ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX idx_tasks_v2_catalog
    ON tasks (workspace_id, archived_at, created_at DESC)
    WHERE definition_id IS NOT NULL;
