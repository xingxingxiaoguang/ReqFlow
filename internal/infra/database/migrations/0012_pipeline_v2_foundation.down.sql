DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS pipeline_cursors;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS retrieval_chunks;
DROP TABLE IF EXISTS retrieval_snapshots;
DROP TABLE IF EXISTS retrieval_profiles;
DROP TABLE IF EXISTS extraction_profiles;

DROP INDEX IF EXISTS idx_dataset_items_incremental;
DROP INDEX IF EXISTS uq_dataset_items_commit_seq;
ALTER TABLE dataset_items
    DROP COLUMN IF EXISTS provenance,
    DROP COLUMN IF EXISTS commit_seq,
    DROP COLUMN IF EXISTS batch_id;

DROP TABLE IF EXISTS dataset_batches;
DROP TABLE IF EXISTS dataset_aliases;

DROP INDEX IF EXISTS idx_datasets_workspace_purpose;
ALTER TABLE datasets
    DROP COLUMN IF EXISTS current_seq,
    DROP COLUMN IF EXISTS key_fields,
    DROP COLUMN IF EXISTS schema_id,
    DROP COLUMN IF EXISTS purpose,
    DROP COLUMN IF EXISTS workspace_id;

DROP TABLE IF EXISTS document_blocks;
DROP TABLE IF EXISTS parsed_documents;
DROP TABLE IF EXISTS asset_set_members;
DROP TABLE IF EXISTS asset_sets;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS task_resource_bindings;
DROP TABLE IF EXISTS step_runs;

ALTER TABLE tasks
    DROP COLUMN IF EXISTS definition_snapshot,
    DROP COLUMN IF EXISTS definition_id,
    DROP COLUMN IF EXISTS workspace_id;
DROP TABLE IF EXISTS task_definitions;
DROP TABLE IF EXISTS dataset_schemas;
