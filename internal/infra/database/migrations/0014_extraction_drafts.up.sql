-- 0014: ExtractionProfile 驱动的分块抽取与候选记录资源。

CREATE TABLE record_draft_sets (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parsed_document_set_id UUID NOT NULL REFERENCES parsed_document_sets (id),
    extraction_profile_id  UUID NOT NULL REFERENCES extraction_profiles (id),
    source_step_run_id      UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    status                  TEXT NOT NULL DEFAULT 'running',
    producer_attempt        INT NOT NULL CHECK (producer_attempt > 0),
    model                   TEXT NOT NULL CHECK (model <> ''),
    unit_count              INT NOT NULL DEFAULT 0 CHECK (unit_count >= 0),
    succeeded_unit_count    INT NOT NULL DEFAULT 0 CHECK (succeeded_unit_count >= 0),
    failed_unit_count       INT NOT NULL DEFAULT 0 CHECK (failed_unit_count >= 0),
    draft_count             INT NOT NULL DEFAULT 0 CHECK (draft_count >= 0),
    llm_request_count       INT NOT NULL DEFAULT 0 CHECK (llm_request_count >= 0),
    input_tokens            BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens           BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens       BIGINT NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens      BIGINT NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at             TIMESTAMPTZ,
    UNIQUE (source_step_run_id),
    CHECK (status IN ('running', 'succeeded', 'partial', 'failed')),
    CHECK (succeeded_unit_count + failed_unit_count <= unit_count)
);
CREATE INDEX idx_record_draft_sets_source
    ON record_draft_sets (parsed_document_set_id, extraction_profile_id, created_at DESC);

CREATE TABLE extraction_units (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    record_draft_set_id   UUID NOT NULL REFERENCES record_draft_sets (id) ON DELETE CASCADE,
    unit_key              TEXT NOT NULL,
    parsed_document_id    UUID NOT NULL REFERENCES parsed_documents (id),
    ordinal               INT NOT NULL,
    first_block_ordinal   INT NOT NULL,
    last_block_ordinal    INT NOT NULL,
    input_hash            TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending',
    error_message         TEXT NOT NULL DEFAULT '',
    response_hash         TEXT NOT NULL DEFAULT '',
    request_count         INT NOT NULL DEFAULT 0 CHECK (request_count >= 0),
    last_request_attempt  INT NOT NULL DEFAULT 0 CHECK (last_request_attempt >= 0),
    last_usage_attempt    INT NOT NULL DEFAULT 0 CHECK (last_usage_attempt >= 0),
    input_tokens          BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens         BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens     BIGINT NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens    BIGINT NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at           TIMESTAMPTZ,
    UNIQUE (record_draft_set_id, unit_key),
    UNIQUE (record_draft_set_id, ordinal),
    CHECK (ordinal >= 0),
    CHECK (unit_key <> '' AND input_hash <> ''),
    CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    CHECK (first_block_ordinal >= 0 AND last_block_ordinal >= first_block_ordinal)
);
CREATE INDEX idx_extraction_units_status
    ON extraction_units (record_draft_set_id, status, ordinal);

CREATE TABLE record_drafts (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    record_draft_set_id  UUID NOT NULL REFERENCES record_draft_sets (id) ON DELETE CASCADE,
    extraction_unit_id   UUID NOT NULL REFERENCES extraction_units (id) ON DELETE CASCADE,
    ordinal              INT NOT NULL,
    fields               JSONB NOT NULL,
    field_confidence     JSONB NOT NULL DEFAULT '{}'::jsonb,
    provenance           JSONB NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (extraction_unit_id, ordinal),
    CHECK (ordinal >= 0),
    CHECK (jsonb_typeof(fields) = 'object'),
    CHECK (jsonb_typeof(field_confidence) = 'object'),
    CHECK (jsonb_typeof(provenance) = 'object')
);
CREATE INDEX idx_record_drafts_set ON record_drafts (record_draft_set_id, extraction_unit_id, ordinal);
