DROP INDEX IF EXISTS idx_tasks_batch;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS chk_tasks_batch_metadata;
ALTER TABLE tasks
    DROP COLUMN IF EXISTS source_filename,
    DROP COLUMN IF EXISTS source_asset_id,
    DROP COLUMN IF EXISTS batch_size,
    DROP COLUMN IF EXISTS batch_ordinal,
    DROP COLUMN IF EXISTS batch_id;
