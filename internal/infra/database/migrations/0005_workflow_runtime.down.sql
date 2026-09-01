ALTER TABLE artifacts
    DROP CONSTRAINT IF EXISTS artifacts_producer_node_fk,
    DROP CONSTRAINT IF EXISTS artifacts_producer_run_fk;
ALTER TABLE analysis_results
    DROP CONSTRAINT IF EXISTS analysis_results_producer_node_fk,
    DROP CONSTRAINT IF EXISTS analysis_results_producer_run_fk;
ALTER TABLE pipeline_cursors DROP CONSTRAINT IF EXISTS pipeline_cursors_last_success_run_fk;
ALTER TABLE retrieval_snapshots DROP CONSTRAINT IF EXISTS retrieval_snapshots_producer_node_fk;
ALTER TABLE approved_record_sets DROP CONSTRAINT IF EXISTS approved_record_sets_producer_node_fk;
ALTER TABLE validation_result_sets DROP CONSTRAINT IF EXISTS validation_result_sets_producer_node_fk;
ALTER TABLE transformed_record_sets DROP CONSTRAINT IF EXISTS transformed_record_sets_producer_node_fk;
ALTER TABLE record_draft_sets DROP CONSTRAINT IF EXISTS record_draft_sets_producer_node_fk;
ALTER TABLE parsed_document_sets DROP CONSTRAINT IF EXISTS parsed_document_sets_producer_node_fk;
ALTER TABLE dataset_batches
    DROP CONSTRAINT IF EXISTS dataset_batches_producer_node_fk,
    DROP CONSTRAINT IF EXISTS dataset_batches_producer_run_fk;

DROP TABLE IF EXISTS node_resource_bindings;
DROP TABLE IF EXISTS workflow_node_runs;
DROP TABLE IF EXISTS workflow_runs;
