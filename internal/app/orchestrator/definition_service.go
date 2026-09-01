package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type DefinitionService struct {
	repo      port.OrchestratorDefinitionRepo
	registry  *Registry
	resources port.TaskResourceResolver
}

func NewDefinitionService(repo port.OrchestratorDefinitionRepo, registry *Registry, resources port.TaskResourceResolver) *DefinitionService {
	return &DefinitionService{repo: repo, registry: registry, resources: resources}
}

func (s *DefinitionService) Create(ctx context.Context, definition model.TaskDefinition) (*model.TaskDefinition, error) {
	definition.Key = strings.TrimSpace(definition.Key)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Description = strings.TrimSpace(definition.Description)
	if definition.Status == "" {
		definition.Status = model.TaskDefinitionDraft
	}
	if definition.Status != model.TaskDefinitionDraft && definition.Status != model.TaskDefinitionActive {
		return nil, fmt.Errorf("新任务定义状态只能是 draft 或 active")
	}
	snapshot, hash, err := logic.NormalizeTaskDefinition(definition)
	if err != nil {
		return nil, err
	}
	if definition.Status == model.TaskDefinitionActive {
		if err := s.registry.ValidateDefinition(ctx, definition); err != nil {
			return nil, err
		}
	}
	definition.DefinitionHash = hash
	if definition.WorkspaceID == "" {
		definition.WorkspaceID = "default"
	}
	if err := s.repo.CreateTaskDefinition(ctx, &definition, snapshot); err != nil {
		return nil, err
	}
	return &definition, nil
}

type CreateTaskInput struct {
	DefinitionID   string
	Title          string
	Bindings       []model.TaskResourceBinding
	Aliases        map[string]string // port_name -> Dataset Alias；解析后不进入任务快照
	StepConfigs    map[string]json.RawMessage
	BatchID        string
	BatchOrdinal   int
	BatchSize      int
	SourceAssetID  string
	SourceFilename string
}

func (s *DefinitionService) CreateTask(ctx context.Context, in CreateTaskInput) (*model.Task, error) {
	execution, err := s.prepareTask(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreateTaskExecution(ctx, execution.Task, execution.Bindings, execution.Steps); err != nil {
		return nil, err
	}
	return execution.Task, nil
}

func (s *DefinitionService) prepareTask(ctx context.Context, in CreateTaskInput) (port.TaskExecutionCreate, error) {
	definition, err := s.repo.GetTaskDefinition(ctx, in.DefinitionID)
	if err != nil {
		return port.TaskExecutionCreate{}, fmt.Errorf("读取任务定义: %w", err)
	}
	if definition.Status != model.TaskDefinitionActive {
		return port.TaskExecutionCreate{}, fmt.Errorf("任务定义 %s 当前状态 %s 不可创建任务", definition.Key, definition.Status)
	}
	if err := applyStepConfigOverrides(definition, in.StepConfigs); err != nil {
		return port.TaskExecutionCreate{}, err
	}
	if err := s.registry.ValidateRuntimeDefinition(ctx, *definition); err != nil {
		return port.TaskExecutionCreate{}, err
	}
	if err := validateInputBindings(*definition, in.Bindings, in.Aliases); err != nil {
		return port.TaskExecutionCreate{}, err
	}
	snapshot, _, err := logic.NormalizeTaskDefinition(*definition)
	if err != nil {
		return port.TaskExecutionCreate{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = definition.Name
	}
	task := &model.Task{
		WorkspaceID:        definition.WorkspaceID,
		DefinitionID:       definition.ID,
		DefinitionSnapshot: string(snapshot),
		Type:               definition.Key, Title: title,
		BatchID: in.BatchID, BatchOrdinal: in.BatchOrdinal, BatchSize: in.BatchSize,
		SourceAssetID: in.SourceAssetID, SourceFilename: in.SourceFilename,
		Status: model.TaskStatusPending,
	}
	steps := make([]model.StepRun, len(definition.Steps))
	for i, step := range definition.Steps {
		configHash := canonicalJSONHash(step.Config)
		steps[i] = model.StepRun{StepID: step.ID, Ordinal: i + 1, Kind: step.Kind, Status: model.StepRunPending, ConfigHash: configHash}
	}
	bindings := make([]model.TaskResourceBinding, len(in.Bindings))
	copy(bindings, in.Bindings)
	for i := range bindings {
		bindings[i].ResourceID = strings.TrimSpace(bindings[i].ResourceID)
		bindings[i].Direction = model.ResourceInput
		if s.resources == nil {
			return port.TaskExecutionCreate{}, fmt.Errorf("任务资源解析器未配置")
		}
		alias := strings.TrimSpace(in.Aliases[bindings[i].PortName])
		resolved, err := s.resources.ResolveTaskResource(ctx, definition.WorkspaceID, bindings[i], alias)
		if err != nil {
			return port.TaskExecutionCreate{}, fmt.Errorf("解析输入端口 %s: %w", bindings[i].PortName, err)
		}
		bindings[i] = resolved
		bindings[i].Direction = model.ResourceInput
	}
	return port.TaskExecutionCreate{Task: task, Bindings: bindings, Steps: steps}, nil
}

// runtimeConfigurableKinds 的步骤允许在创建任务时浅合并覆盖 config：抽取/检索规则
// 本身为数据集字段服务，在任务创建时按目标数据集的 schema 落实。其余步骤的 config
// 属于流程定义本身，不接受任务级改写。
var runtimeConfigurableKinds = map[model.StepKind]bool{
	model.StepKindDocumentExtract: true,
	model.StepKindRetrievalBuild:  true,
}

func applyStepConfigOverrides(definition *model.TaskDefinition, overrides map[string]json.RawMessage) error {
	if len(overrides) == 0 {
		return nil
	}
	byID := make(map[string]*model.StepDefinition, len(definition.Steps))
	for i := range definition.Steps {
		byID[definition.Steps[i].ID] = &definition.Steps[i]
	}
	for stepID, patch := range overrides {
		step, ok := byID[strings.TrimSpace(stepID)]
		if !ok {
			return fmt.Errorf("step_configs 指定了不存在的步骤 %s", stepID)
		}
		if !runtimeConfigurableKinds[step.Kind] {
			return fmt.Errorf("步骤 %s（%s）不支持任务级配置覆盖", stepID, step.Kind)
		}
		base := map[string]any{}
		if len(step.Config) > 0 {
			if err := json.Unmarshal(step.Config, &base); err != nil {
				return fmt.Errorf("步骤 %s 的定义 config 非法: %w", stepID, err)
			}
		}
		override := map[string]any{}
		if len(patch) > 0 {
			if err := json.Unmarshal(patch, &override); err != nil {
				return fmt.Errorf("步骤 %s 的任务级 config 非法: %w", stepID, err)
			}
		}
		for key, value := range override {
			base[key] = value
		}
		merged, err := json.Marshal(base)
		if err != nil {
			return fmt.Errorf("合并步骤 %s 的任务级 config 失败: %w", stepID, err)
		}
		step.Config = merged
	}
	return nil
}

func validateInputBindings(def model.TaskDefinition, bindings []model.TaskResourceBinding, aliases map[string]string) error {
	byPort := make(map[string]model.TaskResourceBinding, len(bindings))
	for _, binding := range bindings {
		if _, exists := byPort[binding.PortName]; exists {
			return fmt.Errorf("输入端口重复绑定: %s", binding.PortName)
		}
		portDef, exists := def.InputPorts[binding.PortName]
		if !exists {
			return fmt.Errorf("任务定义不存在输入端口 %s", binding.PortName)
		}
		if binding.ResourceType != portDef.ResourceType {
			return fmt.Errorf("输入端口 %s 需要 %s，实际为 %s", binding.PortName, portDef.ResourceType, binding.ResourceType)
		}
		if strings.TrimSpace(binding.ResourceID) == "" && strings.TrimSpace(aliases[binding.PortName]) == "" {
			return fmt.Errorf("输入端口 %s 缺少 resource_id 或 resource_alias", binding.PortName)
		}
		byPort[binding.PortName] = binding
	}
	for name, portDef := range def.InputPorts {
		if portDef.Required {
			if _, exists := byPort[name]; !exists {
				return fmt.Errorf("缺少必填输入端口 %s", name)
			}
		}
	}
	return nil
}

func canonicalJSONHash(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	canonical, _ := json.Marshal(value)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
