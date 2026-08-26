-- 0004: 工作流元数据 —— 任务携带自身的工作流定义快照（步骤链 + 依赖声明）。
-- TEXT（JSON 文本，同 input/output 决策）；旧任务为空串，读取时按类型回退注册表。
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS workflow TEXT;
