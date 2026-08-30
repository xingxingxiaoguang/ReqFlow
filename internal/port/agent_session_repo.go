package port

import (
	"context"
	"errors"

	"reqflow/internal/domain/model"
)

var ErrAgentSessionRunning = errors.New("agent session is already running")

// AgentSessionRepo 持久化数字大脑会话，并用条件状态更新防止同一会话并发运行。
type AgentSessionRepo interface {
	CreateAgentSession(ctx context.Context, session *model.AgentSession) error
	ListAgentSessions(ctx context.Context, workspaceID string, limit int) ([]model.AgentSession, error)
	GetAgentSession(ctx context.Context, id string) (*model.AgentSession, error)
	BeginAgentSession(ctx context.Context, id string) error
	SaveAgentSession(ctx context.Context, session *model.AgentSession) error
	RecoverAgentSessions(ctx context.Context) error
}
