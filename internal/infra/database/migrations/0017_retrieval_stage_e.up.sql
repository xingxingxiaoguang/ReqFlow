-- 0017: Stage E 检索快照的 StepRun 幂等身份与 Agent 知识工具审计。

ALTER TABLE retrieval_snapshots
    ADD COLUMN source_step_run_id UUID REFERENCES step_runs (id),
    ADD COLUMN producer_attempt INT NOT NULL DEFAULT 0,
    ADD CONSTRAINT retrieval_snapshots_status_check CHECK (
        status IN ('building', 'validating', 'active', 'failed', 'retired')
    );

CREATE UNIQUE INDEX uq_retrieval_snapshots_step_run
    ON retrieval_snapshots (source_step_run_id)
    WHERE source_step_run_id IS NOT NULL;

CREATE TABLE knowledge_tool_audits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id      TEXT NOT NULL,
    workspace_id  TEXT NOT NULL DEFAULT 'default',
    tool_name     TEXT NOT NULL,
    source_name   TEXT NOT NULL DEFAULT '',
    request       JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_count  INT NOT NULL DEFAULT 0,
    latency_ms    BIGINT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_knowledge_tool_audits_scope
    ON knowledge_tool_audits (scope_id, created_at DESC);
