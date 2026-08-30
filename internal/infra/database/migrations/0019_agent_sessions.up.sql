-- 0019: ReqFlow 数字大脑独立会话。Context 是 pi 式完整消息上下文，允许刷新后续聊。

CREATE TABLE agent_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id TEXT NOT NULL DEFAULT 'default',
    title        TEXT NOT NULL DEFAULT '新会话',
    status       TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'running', 'error')),
    context      JSONB NOT NULL DEFAULT '{"messages":[]}'::jsonb,
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_sessions_workspace
    ON agent_sessions (workspace_id, updated_at DESC);
