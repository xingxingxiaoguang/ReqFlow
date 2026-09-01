ALTER TABLE workflows DROP CONSTRAINT IF EXISTS workflows_active_revision_fk;
DROP TABLE IF EXISTS workflow_previews;
DROP TABLE IF EXISTS workflow_revisions;
