DROP INDEX IF EXISTS idx_tasks_v2_catalog;
ALTER TABLE tasks DROP COLUMN IF EXISTS archived_at;
DROP INDEX IF EXISTS uq_artifacts_source_step;
ALTER TABLE artifacts DROP COLUMN IF EXISTS producer_attempt;
DROP TABLE IF EXISTS analysis_results;
DROP TABLE IF EXISTS analysis_profiles;
