-- 0020: ReqFlow 数字大脑模块设置与纯文本 Skill。Skill 不支持脚本或附件。

CREATE TABLE agent_skills (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    slug         TEXT NOT NULL CHECK (slug ~ '^[a-z][a-z0-9-]{0,47}$'),
    title        TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    description  TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    prompt       TEXT NOT NULL CHECK (char_length(prompt) BETWEEN 1 AND 30000),
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    builtin      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

CREATE INDEX idx_agent_skills_workspace
    ON agent_skills (workspace_id, enabled, updated_at DESC);

CREATE TABLE agent_tool_settings (
    workspace_id TEXT NOT NULL DEFAULT 'default',
    tool_name     TEXT NOT NULL,
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, tool_name)
);
