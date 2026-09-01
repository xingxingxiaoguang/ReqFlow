package model

import (
	"encoding/json"
	"time"
)

// AnalysisProfile 是通用 knowledge.analyze 的不可变执行合同。OutputSchema 同时约束
// LLM 最终输出、Dataset 发布路径和前端结果预览。
type AnalysisProfile struct {
	ID           string
	WorkspaceID  string
	Name         string
	Instruction  string
	OutputSchema json.RawMessage
	ProfileHash  string
	CreatedAt    time.Time
}

type AnalysisResult struct {
	ID                    string
	WorkspaceID           string
	AnalysisProfileID     string
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
