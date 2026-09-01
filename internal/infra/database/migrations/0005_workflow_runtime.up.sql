CREATE TABLE workflow_runs (
    id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows (id),
    revision_id UUID NOT NULL REFERENCES workflow_revisions (id),
    workspace_id TEXT NOT NULL,
    revision JSONB NOT NULL,
    inputs JSONB NOT NULL DEFAULT '[]'::jsonb,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'pausing', 'paused', 'awaiting_manual_completion', 'failed', 'succeeded')),
    current_node_id TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_workflow_runs_workspace ON workflow_runs (workspace_id, created_at DESC);
CREATE INDEX idx_workflow_runs_queue ON workflow_runs (status, updated_at);

CREATE TABLE workflow_node_runs (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    ordinal INT NOT NULL CHECK (ordinal > 0),
    node JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'queued', 'running', 'retry_wait', 'awaiting_manual_completion', 'validating', 'failed', 'succeeded')),
    attempt INT NOT NULL DEFAULT 0,
    checkpoint JSONB NOT NULL DEFAULT '{}'::jsonb,
    progress JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    retry_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    UNIQUE (run_id, node_id),
    UNIQUE (run_id, ordinal)
);
CREATE INDEX idx_workflow_node_runs_queue ON workflow_node_runs (status, retry_at, lease_until, ordinal);

CREATE TABLE node_resource_bindings (
    id UUID PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
    node_run_id UUID NOT NULL REFERENCES workflow_node_runs (id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    port TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('input', 'output')),
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    boundary JSONB NOT NULL DEFAULT '{}'::jsonb,
    provenance JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (node_run_id, direction, port)
);
CREATE INDEX idx_node_resource_bindings_resource ON node_resource_bindings (resource_type, resource_id);
