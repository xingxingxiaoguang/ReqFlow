-- 0002: 分析会话落库（agent 模式与单发模式统一）。
-- agent_context 为 port.Context 的 JSON 文本：refine 微调与换模型续跑的载体。
-- 用 TEXT 而非 JSONB：GORM 字符串参数直写无需类型转换，当前无库内查询需求。
ALTER TABLE import_records ADD COLUMN IF NOT EXISTS agent_context TEXT;
