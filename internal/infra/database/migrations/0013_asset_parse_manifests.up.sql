-- 0013: source.parse 的一等输出 Manifest。
-- parsed_documents 是按 Asset + Parser 版本缓存的单文件结果；Manifest 才是一次
-- StepRun 的多文件输出，保留输入顺序、逐文件失败和 attempt fencing。

CREATE TABLE parsed_document_sets (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_set_id      UUID NOT NULL REFERENCES asset_sets (id),
    source_step_run_id UUID NOT NULL REFERENCES step_runs (id) ON DELETE CASCADE,
    parser_name       TEXT NOT NULL,
    parser_version    TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'running',
    producer_attempt  INT NOT NULL CHECK (producer_attempt > 0),
    total_count       INT NOT NULL DEFAULT 0 CHECK (total_count >= 0),
    succeeded_count   INT NOT NULL DEFAULT 0 CHECK (succeeded_count >= 0),
    failed_count      INT NOT NULL DEFAULT 0 CHECK (failed_count >= 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    UNIQUE (source_step_run_id)
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
