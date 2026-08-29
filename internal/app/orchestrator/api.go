package orchestrator

import (
	"context"
	"encoding/json"
	"time"

	"reqflow/internal/domain/model"
)

// 以下 DTO 是 V2 HTTP 等入站适配器的稳定边界，避免 httpgin 直接依赖 domain。
type PortInput struct {
	ResourceType string `json:"resource_type"`
	Required     bool   `json:"required,omitempty"`
	Description  string `json:"description,omitempty"`
}

type StepDefinitionInput struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	DependsOn []string          `json:"depends_on,omitempty"`
	Inputs    map[string]string `json:"inputs,omitempty"`
	Outputs   map[string]string `json:"outputs,omitempty"`
	Config    json.RawMessage   `json:"config,omitempty"`
}

type CreateDefinitionInput struct {
	WorkspaceID    string                `json:"workspace_id,omitempty"`
	Key            string                `json:"key"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Status         string                `json:"status,omitempty"`
	InputPorts     map[string]PortInput  `json:"input_ports"`
	OutputPorts    map[string]PortInput  `json:"output_ports,omitempty"`
	OutputBindings map[string]string     `json:"output_bindings,omitempty"`
	Steps          []StepDefinitionInput `json:"steps"`
}

type DefinitionView struct {
	ID             string                `json:"id"`
	WorkspaceID    string                `json:"workspace_id"`
	Key            string                `json:"key"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Status         string                `json:"status"`
	InputPorts     map[string]PortInput  `json:"input_ports"`
	OutputPorts    map[string]PortInput  `json:"output_ports,omitempty"`
	OutputBindings map[string]string     `json:"output_bindings,omitempty"`
	Steps          []StepDefinitionInput `json:"steps"`
	DefinitionHash string                `json:"definition_hash"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

func (s *DefinitionService) Register(ctx context.Context, input CreateDefinitionInput) (*DefinitionView, error) {
	definition := definitionFromInput(input)
	created, err := s.Create(ctx, definition)
	if err != nil {
		return nil, err
	}
	view := definitionView(*created)
	return &view, nil
}

type ResourceBindingInput struct {
	PortName      string          `json:"port_name"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id"`
	ResourceAlias string          `json:"resource_alias,omitempty"`
	Boundary      json.RawMessage `json:"boundary,omitempty"`
}

type CreateExecutionInput struct {
	DefinitionID string                 `json:"definition_id"`
	Title        string                 `json:"title,omitempty"`
	Bindings     []ResourceBindingInput `json:"bindings"`
}

func (s *DefinitionService) CreateExecution(ctx context.Context, input CreateExecutionInput) (*TaskView, error) {
	bindings := make([]model.TaskResourceBinding, len(input.Bindings))
	aliases := make(map[string]string)
	for i, binding := range input.Bindings {
		bindings[i] = model.TaskResourceBinding{PortName: binding.PortName,
			ResourceType: model.ResourceType(binding.ResourceType), ResourceID: binding.ResourceID,
			Boundary: binding.Boundary}
		if binding.ResourceAlias != "" {
			aliases[binding.PortName] = binding.ResourceAlias
		}
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{DefinitionID: input.DefinitionID, Title: input.Title, Bindings: bindings, Aliases: aliases})
	if err != nil {
		return nil, err
	}
	return &TaskView{ID: task.ID, WorkspaceID: task.WorkspaceID, DefinitionID: task.DefinitionID,
		Type: task.Type, Title: task.Title, Status: task.Status, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}, nil
}

type ResourceView struct {
	PortName     string          `json:"port_name"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Boundary     json.RawMessage `json:"boundary,omitempty"`
}

type StepRunView struct {
	ID           string          `json:"id"`
	StepID       string          `json:"step_id"`
	Ordinal      int             `json:"ordinal"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Attempt      int             `json:"attempt"`
	InputHash    string          `json:"input_hash,omitempty"`
	ConfigHash   string          `json:"config_hash,omitempty"`
	Progress     json.RawMessage `json:"progress,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	LeaseUntil   time.Time       `json:"lease_until,omitempty"`
	StartedAt    time.Time       `json:"started_at,omitempty"`
	FinishedAt   time.Time       `json:"finished_at,omitempty"`
}

type TaskView struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	DefinitionID string    `json:"definition_id"`
	Type         string    `json:"type"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	CurrentStep  int       `json:"current_step"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
}

type TaskSnapshot struct {
	Task        TaskView                  `json:"task"`
	Inputs      []ResourceView            `json:"inputs"`
	Outputs     []ResourceView            `json:"outputs"`
	Steps       []StepRunView             `json:"steps"`
	StepOutputs map[string][]ResourceView `json:"step_outputs"`
}

func (s *RuntimeService) Snapshot(ctx context.Context, taskID string) (*TaskSnapshot, error) {
	execution, err := s.repo.GetTaskExecution(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return snapshotView(execution), nil
}

func definitionFromInput(input CreateDefinitionInput) model.TaskDefinition {
	definition := model.TaskDefinition{WorkspaceID: input.WorkspaceID, Key: input.Key, Name: input.Name,
		Description: input.Description, Status: input.Status, OutputBindings: input.OutputBindings,
		InputPorts:  make(map[string]model.PortDefinition, len(input.InputPorts)),
		OutputPorts: make(map[string]model.PortDefinition, len(input.OutputPorts)),
		Steps:       make([]model.StepDefinition, len(input.Steps))}
	for name, port := range input.InputPorts {
		definition.InputPorts[name] = model.PortDefinition{ResourceType: model.ResourceType(port.ResourceType), Required: port.Required, Description: port.Description}
	}
	for name, port := range input.OutputPorts {
		definition.OutputPorts[name] = model.PortDefinition{ResourceType: model.ResourceType(port.ResourceType), Required: port.Required, Description: port.Description}
	}
	for i, step := range input.Steps {
		outputs := make(map[string]model.ResourceType, len(step.Outputs))
		for name, resourceType := range step.Outputs {
			outputs[name] = model.ResourceType(resourceType)
		}
		definition.Steps[i] = model.StepDefinition{ID: step.ID, Name: step.Name, Kind: model.StepKind(step.Kind),
			DependsOn: step.DependsOn, Inputs: step.Inputs, Outputs: outputs, Config: step.Config}
	}
	return definition
}

func definitionView(definition model.TaskDefinition) DefinitionView {
	view := DefinitionView{ID: definition.ID, WorkspaceID: definition.WorkspaceID, Key: definition.Key,
		Name: definition.Name, Description: definition.Description, Status: definition.Status,
		DefinitionHash: definition.DefinitionHash, OutputBindings: definition.OutputBindings,
		InputPorts:  make(map[string]PortInput, len(definition.InputPorts)),
		OutputPorts: make(map[string]PortInput, len(definition.OutputPorts)),
		Steps:       make([]StepDefinitionInput, len(definition.Steps)), CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt}
	for name, port := range definition.InputPorts {
		view.InputPorts[name] = PortInput{ResourceType: string(port.ResourceType), Required: port.Required, Description: port.Description}
	}
	for name, port := range definition.OutputPorts {
		view.OutputPorts[name] = PortInput{ResourceType: string(port.ResourceType), Required: port.Required, Description: port.Description}
	}
	for i, step := range definition.Steps {
		outputs := make(map[string]string, len(step.Outputs))
		for name, resourceType := range step.Outputs {
			outputs[name] = string(resourceType)
		}
		view.Steps[i] = StepDefinitionInput{ID: step.ID, Name: step.Name, Kind: string(step.Kind),
			DependsOn: step.DependsOn, Inputs: step.Inputs, Outputs: outputs, Config: step.Config}
	}
	return view
}

func snapshotView(execution *model.TaskExecution) *TaskSnapshot {
	task := execution.Task
	view := &TaskSnapshot{Task: TaskView{ID: task.ID, WorkspaceID: task.WorkspaceID, DefinitionID: task.DefinitionID,
		Type: task.Type, Title: task.Title, Status: task.Status, CurrentStep: task.CurrentStep,
		ErrorMessage: task.ErrorMessage, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
		StartedAt: task.StartedAt, FinishedAt: task.FinishedAt}, StepOutputs: map[string][]ResourceView{}}
	for _, binding := range execution.Inputs {
		resource := ResourceView{PortName: binding.PortName, ResourceType: string(binding.ResourceType), ResourceID: binding.ResourceID, Boundary: binding.Boundary}
		if binding.Direction == model.ResourceOutput {
			view.Outputs = append(view.Outputs, resource)
		} else {
			view.Inputs = append(view.Inputs, resource)
		}
	}
	stepIDByRun := make(map[string]string, len(execution.Steps))
	for _, run := range execution.Steps {
		stepIDByRun[run.ID] = run.StepID
		view.Steps = append(view.Steps, StepRunView{ID: run.ID, StepID: run.StepID, Ordinal: run.Ordinal,
			Kind: string(run.Kind), Status: run.Status, Attempt: run.Attempt, InputHash: run.InputHash,
			ConfigHash: run.ConfigHash, Progress: run.Progress,
			ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
			LeaseUntil: run.LeaseUntil, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt})
	}
	for _, binding := range execution.StepOutputs {
		stepID := stepIDByRun[binding.StepRunID]
		view.StepOutputs[stepID] = append(view.StepOutputs[stepID], ResourceView{PortName: binding.PortName,
			ResourceType: string(binding.ResourceType), ResourceID: binding.ResourceID, Boundary: binding.Boundary})
	}
	return view
}
