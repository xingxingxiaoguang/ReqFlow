package model

import (
	"encoding/json"
	"time"
)

const (
	AgentSessionIdle    = "idle"
	AgentSessionRunning = "running"
	AgentSessionError   = "error"
)

// AgentSession 是 ReqFlow 数字大脑的持久化会话。Context 保存可直接交给 pi 式
// agent loop 继续执行的完整消息上下文；Tools 每次运行时重新注册，不作为长期事实。
type AgentSession struct {
	ID          string
	WorkspaceID string
	Title       string
	Status      string
	Context     json.RawMessage
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
