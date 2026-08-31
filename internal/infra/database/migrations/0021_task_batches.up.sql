ALTER TABLE tasks
    ADD COLUMN batch_id UUID,
    ADD COLUMN batch_ordinal INT NOT NULL DEFAULT 0,
    ADD COLUMN batch_size INT NOT NULL DEFAULT 0,
    ADD COLUMN source_asset_id UUID REFERENCES assets (id),
    ADD COLUMN source_filename TEXT NOT NULL DEFAULT '';

ALTER TABLE tasks ADD CONSTRAINT chk_tasks_batch_metadata CHECK (
    (batch_id IS NULL AND batch_ordinal = 0 AND batch_size = 0 AND source_asset_id IS NULL)
    OR
    (batch_id IS NOT NULL AND batch_ordinal > 0 AND batch_size >= batch_ordinal AND source_asset_id IS NOT NULL)
);

CREATE INDEX idx_tasks_batch ON tasks (batch_id, batch_ordinal) WHERE batch_id IS NOT NULL;
