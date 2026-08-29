-- 0016: 人工审核的一等不可变资源。审核结论覆盖 ValidationResultSet 全量记录，
-- data.publish 只能消费这里固化的最终字段与 provenance。

CREATE TABLE approved_record_sets (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    validation_result_set_id UUID NOT NULL REFERENCES validation_result_sets (id),
    target_dataset_id        UUID NOT NULL REFERENCES datasets (id),
    target_schema_id         UUID NOT NULL REFERENCES dataset_schemas (id),
    source_step_run_id       UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    reviewer                 TEXT NOT NULL CHECK (reviewer <> ''),
    rationale                TEXT NOT NULL CHECK (rationale <> ''),
    review_hash              TEXT NOT NULL CHECK (review_hash <> ''),
    reviewed_through_seq     BIGINT NOT NULL CHECK (reviewed_through_seq >= 0),
    record_count             INT NOT NULL CHECK (record_count > 0),
    approved_count           INT NOT NULL CHECK (approved_count >= 0),
    edited_count             INT NOT NULL CHECK (edited_count >= 0),
    excluded_count           INT NOT NULL CHECK (excluded_count >= 0),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source_step_run_id),
    CHECK (approved_count + edited_count + excluded_count = record_count),
    CHECK (approved_count + edited_count > 0)
);
CREATE INDEX idx_approved_record_sets_source
    ON approved_record_sets (validation_result_set_id, created_at DESC);

CREATE TABLE record_review_decisions (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    approved_record_set_id   UUID NOT NULL REFERENCES approved_record_sets (id) ON DELETE CASCADE,
    validation_result_id     UUID NOT NULL REFERENCES validation_results (id),
    transformed_record_id    UUID NOT NULL REFERENCES transformed_records (id),
    ordinal                  INT NOT NULL CHECK (ordinal >= 0),
    action                   TEXT NOT NULL,
    fields                   JSONB NOT NULL,
    item_key                 TEXT NOT NULL DEFAULT '',
    fingerprint              TEXT NOT NULL DEFAULT '',
    issues                   JSONB NOT NULL DEFAULT '[]'::jsonb,
    provenance               JSONB NOT NULL DEFAULT '{}'::jsonb,
    note                     TEXT NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (approved_record_set_id, validation_result_id),
    UNIQUE (approved_record_set_id, ordinal),
    CHECK (action IN ('approve', 'edit', 'exclude')),
    CHECK (jsonb_typeof(fields) = 'object'),
    CHECK (jsonb_typeof(issues) = 'array'),
    CHECK (jsonb_typeof(provenance) = 'object'),
    CHECK (action = 'exclude' OR (item_key <> '' AND fingerprint <> ''))
);
CREATE INDEX idx_record_review_decisions_publish
    ON record_review_decisions (approved_record_set_id, action, ordinal);
