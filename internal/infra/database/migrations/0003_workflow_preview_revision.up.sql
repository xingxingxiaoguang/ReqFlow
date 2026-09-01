CREATE TABLE workflow_revisions (
    id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    revision_no BIGINT NOT NULL CHECK (revision_no > 0),
    content JSONB NOT NULL,
    content_hash TEXT NOT NULL,
    published_by TEXT NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    UNIQUE (workflow_id, revision_no),
    UNIQUE (content_hash)
);
CREATE INDEX idx_workflow_revisions_workflow ON workflow_revisions (workflow_id, revision_no DESC);
ALTER TABLE workflows ADD CONSTRAINT workflows_active_revision_fk
    FOREIGN KEY (active_revision_id) REFERENCES workflow_revisions (id);

CREATE TABLE workflow_previews (
    id UUID PRIMARY KEY,
    workflow_id UUID NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    draft_revision BIGINT NOT NULL CHECK (draft_revision >= 0),
    status TEXT NOT NULL CHECK (status IN ('passed', 'failed')),
    input_manifest JSONB NOT NULL,
    output_manifest JSONB NOT NULL,
    issues JSONB NOT NULL DEFAULT '[]'::jsonb,
    started_by TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    temporary BOOLEAN NOT NULL DEFAULT true
);
CREATE INDEX idx_workflow_previews_workflow ON workflow_previews (workflow_id, draft_revision, started_at DESC);
