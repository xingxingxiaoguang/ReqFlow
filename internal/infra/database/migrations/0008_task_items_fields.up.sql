-- 草稿字段袋化（M2）：task_items 从 requirement 定型物理列改为 schema 字段袋。
-- fields 用 TEXT（JSON 文本）与 dataset_items.fields / 0003 的 TEXT-not-JSONB 决策一致
-- （GORM 字符串直写免类型转换；无库内查询需求）。
-- 研发阶段无正式数据（零兼容）：推倒重建，不做列迁移。

DROP TABLE task_items;

CREATE TABLE task_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    fields        TEXT NOT NULL,                         -- JSON 文本（schema 字段袋）
    status        TEXT NOT NULL DEFAULT 'pending',      -- pending|success|failed
    error_message TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_task_items_task ON task_items (task_id);
