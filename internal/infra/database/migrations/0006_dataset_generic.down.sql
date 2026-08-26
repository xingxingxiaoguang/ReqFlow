DROP INDEX IF EXISTS uq_dataset_items_key;

ALTER TABLE dataset_items
    DROP COLUMN IF EXISTS item_key,
    DROP COLUMN IF EXISTS fingerprint,
    DROP COLUMN IF EXISTS metadata,
    DROP COLUMN IF EXISTS source_task_id,
    DROP COLUMN IF EXISTS updated_at;

ALTER TABLE datasets
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS schema_version,
    DROP COLUMN IF EXISTS extra,
    DROP COLUMN IF EXISTS updated_at;