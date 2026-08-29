-- 0012: 异构数据管线 V2 基础。
-- 项目尚未上线，本迁移用于 V2 分阶段开发；旧运行路径删除后会压平为新的初始迁移。

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

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

CREATE TABLE task_definitions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    TEXT NOT NULL DEFAULT 'default',
    key             TEXT NOT NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'draft',
    definition      JSONB NOT NULL,
    definition_hash TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, key)
);

ALTER TABLE tasks
    ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'default',
    ADD COLUMN definition_id UUID REFERENCES task_definitions (id),
    ADD COLUMN definition_snapshot JSONB;

CREATE TABLE step_runs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    step_id       TEXT NOT NULL,
    ordinal       INT NOT NULL,
    kind          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    attempt       INT NOT NULL DEFAULT 0,
    input_hash    TEXT NOT NULL DEFAULT '',
    config_hash   TEXT NOT NULL DEFAULT '',
    checkpoint    JSONB NOT NULL DEFAULT '{}'::jsonb,
    progress      JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    lease_owner   TEXT NOT NULL DEFAULT '',
    lease_until   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    UNIQUE (task_id, step_id)
);
CREATE INDEX idx_step_runs_queue ON step_runs (status, lease_until, created_at);

CREATE TABLE step_resource_bindings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    step_run_id   UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    port_name     TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id   UUID NOT NULL,
    boundary      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (step_run_id, port_name)
);
CREATE INDEX idx_step_resource_target ON step_resource_bindings (resource_type, resource_id);

CREATE TABLE task_resource_bindings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    port_name     TEXT NOT NULL,
    direction     TEXT NOT NULL CHECK (direction IN ('input', 'output')),
    resource_type TEXT NOT NULL,
    resource_id   UUID NOT NULL,
    boundary      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (task_id, direction, port_name)
);
CREATE INDEX idx_task_resource_target ON task_resource_bindings (resource_type, resource_id);

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

ALTER TABLE datasets
    ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'default',
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'base',
    ADD COLUMN schema_id UUID REFERENCES dataset_schemas (id),
    ADD COLUMN key_fields JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN current_seq BIGINT NOT NULL DEFAULT 0;
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
    source_task_id     UUID REFERENCES tasks (id),
    source_step_run_id UUID REFERENCES step_runs (id),
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
    ON dataset_batches (source_step_run_id) WHERE source_step_run_id IS NOT NULL;

ALTER TABLE dataset_items
    ADD COLUMN batch_id UUID REFERENCES dataset_batches (id),
    ADD COLUMN commit_seq BIGINT,
    ADD COLUMN provenance JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE UNIQUE INDEX uq_dataset_items_commit_seq
    ON dataset_items (dataset_id, commit_seq) WHERE commit_seq IS NOT NULL;
CREATE INDEX idx_dataset_items_incremental
    ON dataset_items (dataset_id, commit_seq) WHERE commit_seq IS NOT NULL;

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
    source_seq           BIGINT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'building',
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

CREATE TABLE artifacts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       TEXT NOT NULL DEFAULT 'default',
    kind               TEXT NOT NULL,
    name               TEXT NOT NULL,
    blob_uri           TEXT NOT NULL,
    content_hash       TEXT NOT NULL,
    source_task_id     UUID REFERENCES tasks (id),
    source_step_run_id UUID REFERENCES step_runs (id),
    metadata           JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE pipeline_cursors (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pipeline_key          TEXT NOT NULL,
    source_dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    target_dataset_id     UUID NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    processed_through_seq BIGINT NOT NULL DEFAULT 0,
    last_success_task_id  UUID REFERENCES tasks (id),
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
