-- 元数据审计（M3）：写路径必记（METADATA §6）。独立小表，只增不改。

CREATE TABLE metadata_audit (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    action       TEXT NOT NULL,               -- update_schema | update_profile | reset_schema | reset_profile | import
    kind         TEXT NOT NULL,
    key          TEXT NOT NULL,
    from_version INT  NOT NULL DEFAULT 0,     -- 变更前 effective 版本（0 = 无 override，自 seed 起）
    to_version   INT  NOT NULL DEFAULT 0,     -- 变更后 effective 版本
    summary      TEXT NOT NULL DEFAULT '',
    operator     TEXT NOT NULL DEFAULT '',    -- 第三波认证前为空
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_metadata_audit_key ON metadata_audit (kind, key, created_at DESC);
