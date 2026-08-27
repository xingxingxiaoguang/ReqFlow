-- 元数据注册表（M3）：DB 覆盖层——seed（代码内置）→ override（本表）→ effective（运行时合并）。
-- kind+key 定位资产：dataset_schema 按数据集类型、analyze_profile 按任务类型（workflow 为 M4 预留）。
-- 版本历史同表保留（同 (kind,key) 递增，不删）；effective = 同 key 最大 version 行，且该行 enabled
-- （最新版被禁用 = 整体回退 seed）。
-- payload 用 TEXT（JSON 文本）——与 dataset_items.fields / 0008 的 TEXT-not-JSONB 决策一致
-- （GORM 字符串直写；无库内查询需求；设计稿 METADATA §4.3 的 JSONB 依全库惯例收敛为 TEXT）。

CREATE TABLE metadata_registry (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind       TEXT NOT NULL,                 -- dataset_schema | analyze_profile | workflow(M4)
    key        TEXT NOT NULL,                 -- 数据集类型 / 任务类型
    version    INT  NOT NULL,                 -- 同 (kind,key) 内递增
    payload    TEXT NOT NULL,                 -- 该版本完整定义（JSON 文本）
    enabled    BOOLEAN NOT NULL DEFAULT TRUE, -- 最新版 false = 回退 seed（版本历史仍保留）
    summary    TEXT NOT NULL DEFAULT '',      -- 变更说明（审计辅助）
    created_by TEXT NOT NULL DEFAULT '',      -- 第三波认证前为空
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, key, version)
);
CREATE INDEX idx_metadata_registry_key ON metadata_registry (kind, key, version DESC);
