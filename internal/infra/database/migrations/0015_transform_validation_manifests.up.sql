-- 0015: 确定性转换与校验的一等不可变 Manifest。

CREATE TABLE transformed_record_sets (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    record_draft_set_id   UUID NOT NULL REFERENCES record_draft_sets (id) ON DELETE CASCADE,
    extraction_profile_id UUID NOT NULL REFERENCES extraction_profiles (id),
    target_schema_id      UUID NOT NULL REFERENCES dataset_schemas (id),
    source_step_run_id    UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    status                TEXT NOT NULL DEFAULT 'running',
    producer_attempt      INT NOT NULL CHECK (producer_attempt > 0),
    engine_version        TEXT NOT NULL CHECK (engine_version <> ''),
    draft_count           INT NOT NULL CHECK (draft_count >= 0),
    transformed_count     INT NOT NULL DEFAULT 0 CHECK (transformed_count >= 0),
    changed_record_count  INT NOT NULL DEFAULT 0 CHECK (changed_record_count >= 0),
    issue_count           INT NOT NULL DEFAULT 0 CHECK (issue_count >= 0),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at           TIMESTAMPTZ,
    UNIQUE (source_step_run_id),
    CHECK (status IN ('running', 'succeeded')),
    CHECK (transformed_count <= draft_count AND changed_record_count <= transformed_count)
);
CREATE INDEX idx_transformed_record_sets_source
    ON transformed_record_sets (record_draft_set_id, created_at DESC);

CREATE TABLE transformed_records (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transformed_record_set_id UUID NOT NULL REFERENCES transformed_record_sets (id) ON DELETE CASCADE,
    record_draft_id           UUID NOT NULL REFERENCES record_drafts (id) ON DELETE CASCADE,
    ordinal                   INT NOT NULL CHECK (ordinal >= 0),
    fields                    JSONB NOT NULL,
    changes                   JSONB NOT NULL DEFAULT '[]'::jsonb,
    issues                    JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (transformed_record_set_id, record_draft_id),
    UNIQUE (transformed_record_set_id, ordinal),
    CHECK (jsonb_typeof(fields) = 'object'),
    CHECK (jsonb_typeof(changes) = 'array'),
    CHECK (jsonb_typeof(issues) = 'array')
);
CREATE INDEX idx_transformed_records_set
    ON transformed_records (transformed_record_set_id, ordinal);

CREATE TABLE validation_result_sets (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transformed_record_set_id UUID NOT NULL REFERENCES transformed_record_sets (id) ON DELETE CASCADE,
    target_dataset_id         UUID NOT NULL REFERENCES datasets (id),
    target_schema_id          UUID NOT NULL REFERENCES dataset_schemas (id),
    source_step_run_id        UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    status                    TEXT NOT NULL DEFAULT 'running',
    producer_attempt          INT NOT NULL CHECK (producer_attempt > 0),
    engine_version            TEXT NOT NULL CHECK (engine_version <> ''),
    validated_through_seq     BIGINT NOT NULL CHECK (validated_through_seq >= 0),
    record_count              INT NOT NULL CHECK (record_count >= 0),
    valid_count               INT NOT NULL DEFAULT 0 CHECK (valid_count >= 0),
    warning_count             INT NOT NULL DEFAULT 0 CHECK (warning_count >= 0),
    invalid_count             INT NOT NULL DEFAULT 0 CHECK (invalid_count >= 0),
    duplicate_count           INT NOT NULL DEFAULT 0 CHECK (duplicate_count >= 0),
    conflict_count            INT NOT NULL DEFAULT 0 CHECK (conflict_count >= 0),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at               TIMESTAMPTZ,
    UNIQUE (source_step_run_id),
    CHECK (status IN ('running', 'succeeded')),
    CHECK (valid_count + warning_count + invalid_count + duplicate_count + conflict_count <= record_count)
);
CREATE INDEX idx_validation_result_sets_source
    ON validation_result_sets (transformed_record_set_id, target_dataset_id, created_at DESC);

CREATE TABLE validation_results (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    validation_result_set_id  UUID NOT NULL REFERENCES validation_result_sets (id) ON DELETE CASCADE,
    transformed_record_id     UUID NOT NULL REFERENCES transformed_records (id) ON DELETE CASCADE,
    ordinal                   INT NOT NULL CHECK (ordinal >= 0),
    fields                    JSONB NOT NULL,
    item_key                  TEXT NOT NULL DEFAULT '',
    fingerprint               TEXT NOT NULL DEFAULT '',
    status                    TEXT NOT NULL,
    issues                    JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (validation_result_set_id, transformed_record_id),
    UNIQUE (validation_result_set_id, ordinal),
    CHECK (jsonb_typeof(fields) = 'object'),
    CHECK (jsonb_typeof(issues) = 'array'),
    CHECK (status IN ('valid', 'warning', 'invalid', 'duplicate_in_batch', 'conflict_existing_key'))
);
CREATE INDEX idx_validation_results_set_status
    ON validation_results (validation_result_set_id, status, ordinal);
