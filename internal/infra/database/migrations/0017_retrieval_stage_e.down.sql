DROP TABLE IF EXISTS knowledge_tool_audits;
DROP INDEX IF EXISTS uq_retrieval_snapshots_step_run;
ALTER TABLE retrieval_snapshots
    DROP CONSTRAINT IF EXISTS retrieval_snapshots_status_check,
    DROP COLUMN IF EXISTS producer_attempt,
    DROP COLUMN IF EXISTS source_step_run_id;
