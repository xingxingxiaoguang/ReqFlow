-- 0022: 平台外部能力配置。配置文件只作为只读兜底，不写入本表。

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

-- 数据库配置同类最多激活一份；当没有激活行时由配置文件兜底项生效。
CREATE UNIQUE INDEX uq_platform_configs_active
    ON platform_configs (workspace_id, kind)
    WHERE is_active;

CREATE INDEX idx_platform_configs_catalog
    ON platform_configs (workspace_id, kind, is_active DESC, updated_at DESC);
