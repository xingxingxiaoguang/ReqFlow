package model

import (
	"encoding/json"
	"time"
)

const (
	StepKindSourceParse    StepKind = "source.parse"
	StepKindLLMExtract     StepKind = "llm.extract"
	StepKindDataTransform  StepKind = "data.transform"
	StepKindDataValidate   StepKind = "data.validate"
	StepKindDataPublish    StepKind = "data.publish"
	StepKindRetrievalBuild StepKind = "retrieval.build"
	StepKindAgentAnalyze   StepKind = "agent.analyze"
	StepKindArtifactRender StepKind = "artifact.render"
	StepKindGraphBuild     StepKind = "graph.build"
	StepKindHumanReview    StepKind = "human.review"
)

// StepDefinition 的输入引用使用 $task.<port> 或 $step.<step_id>.<port>。
type StepDefinition struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Kind      StepKind                `json:"kind"`
	DependsOn []string                `json:"depends_on,omitempty"`
	Inputs    map[string]string       `json:"inputs,omitempty"`
	Outputs   map[string]ResourceType `json:"outputs,omitempty"`
	Config    json.RawMessage         `json:"config,omitempty"`
}

// TaskDefinition 描述一个可复用 SOP。Task 创建时保存完整定义快照。
type TaskDefinition struct {
	ID             string                    `json:"id,omitempty"`
	WorkspaceID    string                    `json:"workspace_id,omitempty"`
	Key            string                    `json:"key"`
	Name           string                    `json:"name"`
	Description    string                    `json:"description,omitempty"`
	Status         string                    `json:"status"`
	InputPorts     map[string]PortDefinition `json:"input_ports"`
	OutputPorts    map[string]PortDefinition `json:"output_ports,omitempty"`
	OutputBindings map[string]string         `json:"output_bindings,omitempty"`
	Steps          []StepDefinition          `json:"steps"`
	DefinitionHash string                    `json:"definition_hash,omitempty"`
	CreatedAt      time.Time                 `json:"created_at,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at,omitempty"`
}

const (
	TaskDefinitionDraft   = "draft"
	TaskDefinitionActive  = "active"
	TaskDefinitionRetired = "retired"
)

// StepRun 是 V2 执行状态的唯一真相源。
type StepRun struct {
	ID           string
	TaskID       string
	StepID       string
	Ordinal      int
	Kind         StepKind
	Status       string
	Attempt      int
	InputHash    string
	ConfigHash   string
	Checkpoint   json.RawMessage
	Progress     json.RawMessage
	ErrorCode    string
	ErrorMessage string
	LeaseOwner   string
	LeaseUntil   time.Time
	CreatedAt    time.Time
	StartedAt    time.Time
	FinishedAt   time.Time
}

// TaskExecution 是 Orchestrator 的持久化聚合快照。DefinitionSnapshot、输入绑定、
// StepRun 和步骤输出共同决定下一次调度，不依赖进程内状态或 Broker 事件。
type TaskExecution struct {
	Task        Task
	Inputs      []TaskResourceBinding
	Steps       []StepRun
	StepOutputs []StepResourceBinding
}

const (
	StepRunPending   = "pending"
	StepRunQueued    = "queued"
	StepRunRunning   = "running"
	StepRunAwaiting  = "awaiting"
	StepRunPaused    = "paused"
	StepRunSucceeded = "succeeded"
	StepRunFailed    = "failed"
	StepRunSkipped   = "skipped"
)
