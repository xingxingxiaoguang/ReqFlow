CREATE TABLE workflow_design_sessions (
    id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    draft_revision BIGINT NOT NULL CHECK (draft_revision >= 0),
    status TEXT NOT NULL,
    session JSONB NOT NULL,
    agent_state JSONB NOT NULL,
    trace JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_workflow_design_sessions_workflow ON workflow_design_sessions (workflow_id, updated_at DESC);
