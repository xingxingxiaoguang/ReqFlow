CREATE TABLE workflows (
    id UUID PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    draft_revision BIGINT NOT NULL DEFAULT 0 CHECK (draft_revision >= 0),
    draft_document JSONB NOT NULL,
    active_revision_id UUID,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (workspace_id, key)
);
CREATE INDEX idx_workflows_workspace_updated ON workflows (workspace_id, updated_at DESC);

CREATE TABLE workflow_command_events (
    id UUID PRIMARY KEY,
    command_id TEXT NOT NULL UNIQUE,
    workflow_id UUID NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    base_revision BIGINT NOT NULL CHECK (base_revision >= 0),
    result_revision BIGINT NOT NULL CHECK (result_revision > base_revision),
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    command_type TEXT NOT NULL,
    command_payload JSONB NOT NULL,
    result_document JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (workflow_id, result_revision)
);
CREATE INDEX idx_workflow_command_events_workflow ON workflow_command_events (workflow_id, result_revision DESC);
