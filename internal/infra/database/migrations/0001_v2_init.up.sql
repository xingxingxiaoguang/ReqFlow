-- 0001: ReqFlow V2 初始迁移（压平版）。
-- 项目未上线：旧任务运行时、元数据注册表、Legacy 数据集列与归档表已随代码删除，
-- 本文件是唯一事实源——全新数据库执行本迁移即得到完整 V2 形状。

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

/* ---- 不可变数据合同 ---- */

CREATE TABLE dataset_schemas (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    json_schema  JSONB NOT NULL,
    ui_schema    JSONB NOT NULL DEFAULT '{}'::jsonb,
    schema_hash  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dataset_schemas_workspace ON dataset_schemas (workspace_id, created_at DESC);
CREATE INDEX idx_dataset_schemas_hash ON dataset_schemas (schema_hash);

/* ---- 原始资产 ---- */

CREATE TABLE assets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    filename     TEXT NOT NULL,
    mime_type    TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256       TEXT NOT NULL,
    blob_uri     TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, sha256)
);

CREATE TABLE asset_sets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    name         TEXT NOT NULL,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE asset_set_members (
    asset_set_id UUID NOT NULL REFERENCES asset_sets (id) ON DELETE CASCADE,
    asset_id     UUID NOT NULL REFERENCES assets (id),
    ordinal      INT NOT NULL,
    PRIMARY KEY (asset_set_id, asset_id),
    UNIQUE (asset_set_id, ordinal)
);

/* ---- Dataset（不可变 Schema + 只追加 Batch） ---- */

CREATE TABLE datasets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    purpose      TEXT NOT NULL DEFAULT 'base'
                 CHECK (purpose IN ('base', 'query', 'analysis', 'graph_node', 'graph_edge')),
    name         TEXT NOT NULL,
    schema_id    UUID NOT NULL REFERENCES dataset_schemas (id),
    key_fields   JSONB NOT NULL DEFAULT '[]'::jsonb,
    description  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'sealed', 'archived')),
    item_count   INT NOT NULL DEFAULT 0,
    current_seq  BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_datasets_workspace_purpose ON datasets (workspace_id, purpose, created_at DESC);

CREATE TABLE dataset_aliases (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      TEXT NOT NULL DEFAULT 'default',
    name              TEXT NOT NULL,
    active_dataset_id UUID NOT NULL REFERENCES datasets (id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE dataset_batches (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id         UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    producer_workflow_run_id UUID,
    producer_node_run_id     UUID,
    status             TEXT NOT NULL DEFAULT 'staging',
    item_count         INT NOT NULL DEFAULT 0,
    from_seq           BIGINT NOT NULL DEFAULT 0,
    to_seq             BIGINT NOT NULL DEFAULT 0,
    payload_hash       TEXT NOT NULL DEFAULT '',
    error_message      TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at       TIMESTAMPTZ,
    CHECK (from_seq >= 0 AND to_seq >= 0)
);
CREATE INDEX idx_dataset_batches_dataset ON dataset_batches (dataset_id, created_at DESC);
CREATE UNIQUE INDEX uq_dataset_batches_source_step
    ON dataset_batches (producer_node_run_id) WHERE producer_node_run_id IS NOT NULL;

CREATE TABLE dataset_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id  UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    batch_id    UUID NOT NULL REFERENCES dataset_batches (id),
    fields      JSONB NOT NULL,
    item_key    TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    commit_seq  BIGINT NOT NULL,
    provenance  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dataset_id, item_key),
    UNIQUE (dataset_id, commit_seq)
);
CREATE INDEX idx_dataset_items_incremental ON dataset_items (dataset_id, commit_seq);

/* ---- 结构化解析 ---- */

CREATE TABLE parsed_documents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id       UUID NOT NULL REFERENCES assets (id) ON DELETE CASCADE,
    parser_name    TEXT NOT NULL,
    parser_version TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending',
    content_hash   TEXT NOT NULL DEFAULT '',
    error_message  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (asset_id, parser_name, parser_version)
);

CREATE TABLE document_blocks (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parsed_document_id UUID NOT NULL REFERENCES parsed_documents (id) ON DELETE CASCADE,
    ordinal            INT NOT NULL,
    block_type         TEXT NOT NULL,
    page_no            INT NOT NULL DEFAULT 0,
    section_path       TEXT NOT NULL DEFAULT '',
    text               TEXT NOT NULL,
    metadata           JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (parsed_document_id, ordinal)
);
CREATE INDEX idx_document_blocks_document ON document_blocks (parsed_document_id, ordinal);

CREATE TABLE parsed_document_sets (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_set_id       UUID NOT NULL REFERENCES asset_sets (id),
    producer_node_run_id UUID NOT NULL,
    parser_name        TEXT NOT NULL,
    parser_version     TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'running',
    producer_attempt   INT NOT NULL CHECK (producer_attempt > 0),
    total_count        INT NOT NULL DEFAULT 0 CHECK (total_count >= 0),
    succeeded_count    INT NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count       INT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at        TIMESTAMPTZ,
    UNIQUE (producer_node_run_id)
);
CREATE INDEX idx_parsed_document_sets_asset_set
    ON parsed_document_sets (asset_set_id, created_at DESC);

CREATE TABLE parsed_document_set_items (
    parsed_document_set_id UUID NOT NULL REFERENCES parsed_document_sets (id) ON DELETE CASCADE,
    asset_id               UUID NOT NULL REFERENCES assets (id),
    parsed_document_id     UUID REFERENCES parsed_documents (id),
    ordinal                INT NOT NULL,
    status                 TEXT NOT NULL DEFAULT 'pending',
    error_message          TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (parsed_document_set_id, asset_id),
    UNIQUE (parsed_document_set_id, ordinal)
);

/* ---- 抽取（Profile 驱动的 Agent 草稿） ---- */

CREATE TABLE extraction_profiles (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        TEXT NOT NULL DEFAULT 'default',
    name                TEXT NOT NULL,
    target_schema_id    UUID NOT NULL REFERENCES dataset_schemas (id),
    record_granularity  TEXT NOT NULL,
    system_instruction  TEXT NOT NULL,
    field_guides        JSONB NOT NULL DEFAULT '{}'::jsonb,
    examples            JSONB NOT NULL DEFAULT '[]'::jsonb,
    normalization_rules JSONB NOT NULL DEFAULT '[]'::jsonb,
    validation_rules    JSONB NOT NULL DEFAULT '[]'::jsonb,
    profile_hash        TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE record_draft_sets (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parsed_document_set_id UUID NOT NULL REFERENCES parsed_document_sets (id),
    extraction_profile_id  UUID NOT NULL REFERENCES extraction_profiles (id),
    producer_node_run_id     UUID NOT NULL,
    status                 TEXT NOT NULL DEFAULT 'running',
    producer_attempt       INT NOT NULL CHECK (producer_attempt > 0),
    model                  TEXT NOT NULL CHECK (model <> ''),
    unit_count             INT NOT NULL DEFAULT 0 CHECK (unit_count >= 0),
    succeeded_unit_count   INT NOT NULL DEFAULT 0 CHECK (succeeded_unit_count >= 0),
    failed_unit_count      INT NOT NULL DEFAULT 0 CHECK (failed_unit_count >= 0),
    draft_count            INT NOT NULL DEFAULT 0 CHECK (draft_count >= 0),
    llm_request_count      INT NOT NULL DEFAULT 0 CHECK (llm_request_count >= 0),
    input_tokens           BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens          BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read_tokens      BIGINT NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens     BIGINT NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at            TIMESTAMPTZ,
    UNIQUE (producer_node_run_id),
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

/* ---- 确定性转换与校验 ---- */

CREATE TABLE transformed_record_sets (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    record_draft_set_id   UUID NOT NULL REFERENCES record_draft_sets (id) ON DELETE CASCADE,
    extraction_profile_id UUID NOT NULL REFERENCES extraction_profiles (id),
    target_schema_id      UUID NOT NULL REFERENCES dataset_schemas (id),
    producer_node_run_id    UUID NOT NULL,
    status                TEXT NOT NULL DEFAULT 'running',
    producer_attempt      INT NOT NULL CHECK (producer_attempt > 0),
    engine_version        TEXT NOT NULL CHECK (engine_version <> ''),
    draft_count           INT NOT NULL CHECK (draft_count >= 0),
    transformed_count     INT NOT NULL DEFAULT 0 CHECK (transformed_count >= 0),
    changed_record_count  INT NOT NULL DEFAULT 0 CHECK (changed_record_count >= 0),
    issue_count           INT NOT NULL DEFAULT 0 CHECK (issue_count >= 0),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at           TIMESTAMPTZ,
    UNIQUE (producer_node_run_id),
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
    producer_node_run_id        UUID NOT NULL,
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
    UNIQUE (producer_node_run_id),
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

/* ---- 人工审核（不可变 ApprovedRecordSet） ---- */

CREATE TABLE approved_record_sets (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    validation_result_set_id UUID NOT NULL REFERENCES validation_result_sets (id),
    target_dataset_id        UUID NOT NULL REFERENCES datasets (id),
    target_schema_id         UUID NOT NULL REFERENCES dataset_schemas (id),
    producer_node_run_id       UUID NOT NULL,
    reviewer                 TEXT NOT NULL CHECK (reviewer <> ''),
    rationale                TEXT NOT NULL CHECK (rationale <> ''),
    review_hash              TEXT NOT NULL CHECK (review_hash <> ''),
    reviewed_through_seq     BIGINT NOT NULL CHECK (reviewed_through_seq >= 0),
    record_count             INT NOT NULL CHECK (record_count > 0),
    approved_count           INT NOT NULL CHECK (approved_count >= 0),
    edited_count             INT NOT NULL CHECK (edited_count >= 0),
    excluded_count           INT NOT NULL CHECK (excluded_count >= 0),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (producer_node_run_id),
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

/* ---- 混合检索（BM25 + pgvector 快照） ---- */

CREATE TABLE retrieval_profiles (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      TEXT NOT NULL DEFAULT 'default',
    name              TEXT NOT NULL,
    dataset_schema_id UUID NOT NULL REFERENCES dataset_schemas (id),
    lexical_config    JSONB NOT NULL,
    vector_config     JSONB NOT NULL,
    filter_fields     JSONB NOT NULL DEFAULT '[]'::jsonb,
    fusion_config     JSONB NOT NULL,
    profile_hash      TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE retrieval_snapshots (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id           UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    retrieval_profile_id UUID NOT NULL REFERENCES retrieval_profiles (id),
    producer_node_run_id   UUID,
    producer_attempt     INT NOT NULL DEFAULT 0,
    source_seq           BIGINT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'building'
                         CHECK (status IN ('building', 'validating', 'active', 'failed', 'retired')),
    lexical_ref          TEXT NOT NULL DEFAULT '',
    vector_ref           TEXT NOT NULL DEFAULT '',
    lexical_count        INT NOT NULL DEFAULT 0,
    vector_count         INT NOT NULL DEFAULT 0,
    failure_reason       TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at         TIMESTAMPTZ
);
CREATE INDEX idx_retrieval_snapshots_lookup
    ON retrieval_snapshots (dataset_id, retrieval_profile_id, status, source_seq DESC);
CREATE UNIQUE INDEX uq_retrieval_snapshots_step_run
    ON retrieval_snapshots (producer_node_run_id)
    WHERE producer_node_run_id IS NOT NULL;

CREATE TABLE retrieval_chunks (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dataset_id           UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    dataset_item_id      UUID NOT NULL REFERENCES dataset_items (id) ON DELETE CASCADE,
    retrieval_profile_id UUID NOT NULL REFERENCES retrieval_profiles (id),
    chunk_no             INT NOT NULL,
    chunk_text           TEXT NOT NULL,
    chunk_hash           TEXT NOT NULL,
    source_seq           BIGINT NOT NULL,
    embedding_model      TEXT NOT NULL,
    embedding            vector(1024),
    metadata             JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dataset_item_id, retrieval_profile_id, chunk_no)
);
CREATE INDEX idx_retrieval_chunks_incremental
    ON retrieval_chunks (dataset_id, retrieval_profile_id, source_seq);
CREATE INDEX idx_retrieval_chunks_hnsw
    ON retrieval_chunks USING hnsw (embedding vector_cosine_ops);

/* ---- 增量消费位点与事件出口 ---- */

CREATE TABLE pipeline_cursors (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_key          TEXT NOT NULL,
    source_dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    target_dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    processed_through_seq BIGINT NOT NULL DEFAULT 0,
    last_success_run_id    UUID,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (pipeline_key, source_dataset_id, target_dataset_id)
);

CREATE TABLE outbox_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic          TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    payload        JSONB NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending',
    attempts       INT NOT NULL DEFAULT 0,
    available_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at   TIMESTAMPTZ
);
CREATE INDEX idx_outbox_events_pending ON outbox_events (status, available_at, created_at);

/* ---- 通用分析与制品 ---- */

CREATE TABLE analysis_profiles (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  TEXT NOT NULL DEFAULT 'default',
    name          TEXT NOT NULL,
    instruction   TEXT NOT NULL,
    output_schema JSONB NOT NULL,
    profile_hash  TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_analysis_profiles_workspace
    ON analysis_profiles (workspace_id, created_at DESC);

CREATE TABLE analysis_results (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        TEXT NOT NULL DEFAULT 'default',
    analysis_profile_id UUID NOT NULL REFERENCES analysis_profiles (id),
    producer_workflow_run_id UUID NOT NULL,
    producer_node_run_id     UUID NOT NULL,
    producer_attempt    INT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    output              JSONB NOT NULL DEFAULT '{}'::jsonb,
    agent_context       JSONB NOT NULL DEFAULT '{}'::jsonb,
    model               TEXT NOT NULL DEFAULT '',
    input_tokens        INT NOT NULL DEFAULT 0,
    output_tokens       INT NOT NULL DEFAULT 0,
    cache_read_tokens   INT NOT NULL DEFAULT 0,
    cache_write_tokens  INT NOT NULL DEFAULT 0,
    error_message       TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at         TIMESTAMPTZ,
    UNIQUE (producer_node_run_id)
);
CREATE INDEX idx_analysis_results_task
    ON analysis_results (producer_workflow_run_id, created_at DESC);

CREATE TABLE artifacts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       TEXT NOT NULL DEFAULT 'default',
    kind               TEXT NOT NULL,
    name               TEXT NOT NULL,
    blob_uri           TEXT NOT NULL,
    content_hash       TEXT NOT NULL,
    producer_workflow_run_id UUID,
    producer_node_run_id     UUID,
    producer_attempt   INT NOT NULL DEFAULT 0,
    metadata           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_artifacts_source_step
    ON artifacts (producer_node_run_id)
    WHERE producer_node_run_id IS NOT NULL;

/* ---- Agent 知识工具审计 ---- */

CREATE TABLE knowledge_tool_audits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_id      TEXT NOT NULL,
    workspace_id  TEXT NOT NULL DEFAULT 'default',
    tool_name     TEXT NOT NULL,
    source_name   TEXT NOT NULL DEFAULT '',
    request       JSONB NOT NULL DEFAULT '{}'::jsonb,
    result_count  INT NOT NULL DEFAULT 0,
    latency_ms    BIGINT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_knowledge_tool_audits_scope
    ON knowledge_tool_audits (scope_id, created_at DESC);

/* ---- 平台外部能力配置（config.yaml 只作只读兜底） ---- */

CREATE TABLE platform_configs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      TEXT NOT NULL DEFAULT 'default',
    kind              TEXT NOT NULL CHECK (kind IN ('llm', 'embedding', 'rerank', 'mineru')),
    name              TEXT NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    settings          JSONB NOT NULL CHECK (jsonb_typeof(settings) = 'object'),
    secret_ciphertext TEXT NOT NULL CHECK (secret_ciphertext <> ''),
    is_active         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_platform_configs_name
    ON platform_configs (workspace_id, kind, lower(name));
CREATE UNIQUE INDEX uq_platform_configs_active
    ON platform_configs (workspace_id, kind)
    WHERE is_active;
CREATE INDEX idx_platform_configs_catalog
    ON platform_configs (workspace_id, kind, is_active DESC, updated_at DESC);
