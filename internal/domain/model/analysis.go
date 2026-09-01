package model

import (
	"encoding/json"
	"time"
)

type AnalysisResult struct {
	ID                    string
	WorkspaceID           string
	Instruction           string
	OutputContract        json.RawMessage
	OutputContractHash    string
	OutputSchema          json.RawMessage
	OutputSchemaHash      string
	ProducerWorkflowRunID string
	ProducerNodeRunID     string
	ProducerAttempt       int
	Status                string
	Output                json.RawMessage
	AgentContext          json.RawMessage
	Model                 string
	InputTokens           int
	OutputTokens          int
	CacheReadTokens       int
	CacheWriteTokens      int
	ErrorMessage          string
	CreatedAt             time.Time
	FinishedAt            time.Time
}

const (
	AnalysisResultRunning   = "running"
	AnalysisResultSucceeded = "succeeded"
	AnalysisResultFailed    = "failed"
)
