package port

import (
	"context"
	"encoding/json"
	"errors"

	domain "reqflow/internal/domain/workflow"
)

var ErrDesignSessionNotFound = errors.New("workflow design session not found")

type DesignSessionRecord struct {
	Session    domain.DesignSession
	AgentState json.RawMessage
	Trace      json.RawMessage
}

type WorkflowDesignRepo interface {
	CreateDesignSession(ctx context.Context, record DesignSessionRecord) error
	GetDesignSession(ctx context.Context, id string) (*DesignSessionRecord, error)
	SaveDesignSession(ctx context.Context, record DesignSessionRecord) error
}
