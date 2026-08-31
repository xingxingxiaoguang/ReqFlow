package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const maxTaskBatchFiles = 100

type TaskBatchAssetService interface {
	GetAssetSet(ctx context.Context, id string) (*model.AssetSet, []model.AssetSetEntry, error)
	CreateSingleAssetSet(ctx context.Context, workspaceID, name, createdBy, assetID string) (*model.AssetSet, error)
}

type TaskBatchService struct {
	definitions *DefinitionService
	assets      TaskBatchAssetService
}

func NewTaskBatchService(definitions *DefinitionService, assets TaskBatchAssetService) (*TaskBatchService, error) {
	if definitions == nil || assets == nil {
		return nil, fmt.Errorf("任务批次服务依赖不完整")
	}
	return &TaskBatchService{definitions: definitions, assets: assets}, nil
}

type CreateBatchExecutionInput struct {
	DefinitionID  string                 `json:"definition_id"`
	Title         string                 `json:"title,omitempty"`
	Bindings      []ResourceBindingInput `json:"bindings"`
	SplitPortName string                 `json:"split_port_name"`
	StartNow      bool                   `json:"start_now,omitempty"`
}

type TaskBatchView struct {
	ID    string     `json:"id"`
	Size  int        `json:"size"`
	Tasks []TaskView `json:"tasks"`
}

func (s *TaskBatchService) CreateExecutions(ctx context.Context, input CreateBatchExecutionInput) (*TaskBatchView, error) {
	definition, err := s.definitions.repo.GetTaskDefinition(ctx, strings.TrimSpace(input.DefinitionID))
	if err != nil {
		return nil, fmt.Errorf("读取任务定义: %w", err)
	}
	splitPortName := strings.TrimSpace(input.SplitPortName)
	portDefinition, ok := definition.InputPorts[splitPortName]
	if !ok || portDefinition.ResourceType != model.ResourceAssetSet {
		return nil, fmt.Errorf("拆分端口 %s 必须是流程中的文件集输入", splitPortName)
	}
	splitBindingIndex := -1
	for i := range input.Bindings {
		if input.Bindings[i].PortName == splitPortName {
			if input.Bindings[i].ResourceType != string(model.ResourceAssetSet) {
				return nil, fmt.Errorf("拆分端口 %s 必须绑定文件集", splitPortName)
			}
			if input.Bindings[i].ResourceAlias != "" {
				return nil, fmt.Errorf("按文件拆分不支持文件集别名")
			}
			splitBindingIndex = i
			break
		}
	}
	if splitBindingIndex < 0 {
		return nil, fmt.Errorf("缺少待拆分的文件集输入 %s", splitPortName)
	}
	originalSet, entries, err := s.assets.GetAssetSet(ctx, input.Bindings[splitBindingIndex].ResourceID)
	if err != nil {
		return nil, fmt.Errorf("读取待拆分文件集: %w", err)
	}
	if originalSet.WorkspaceID != definition.WorkspaceID {
		return nil, fmt.Errorf("文件集与流程不属于同一工作区")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("文件集为空，无法创建批量任务")
	}
	if len(entries) > maxTaskBatchFiles {
		return nil, fmt.Errorf("单个任务批次最多包含 %d 个文件", maxTaskBatchFiles)
	}

	batchID := uuid.NewString()
	baseTitle := strings.TrimSpace(input.Title)
	if baseTitle == "" {
		baseTitle = definition.Name
	}
	executions := make([]port.TaskExecutionCreate, 0, len(entries))
	for i, entry := range entries {
		singletonName := fmt.Sprintf("%s · %s", originalSet.Name, entry.Asset.Filename)
		singleton, err := s.assets.CreateSingleAssetSet(ctx, originalSet.WorkspaceID,
			singletonName, "task_batch:"+batchID, entry.Asset.ID)
		if err != nil {
			return nil, fmt.Errorf("为文件 %s 创建独立输入: %w", entry.Asset.Filename, err)
		}
		bindings := cloneResourceBindingInputs(input.Bindings)
		bindings[splitBindingIndex].ResourceID = singleton.ID
		bindings[splitBindingIndex].Boundary = json.RawMessage(`{}`)
		prepared, err := s.definitions.prepareTask(ctx, createTaskInput(CreateExecutionInput{
			DefinitionID: input.DefinitionID,
			Title:        fmt.Sprintf("%s · %s", baseTitle, entry.Asset.Filename),
			Bindings:     bindings,
		}))
		if err != nil {
			return nil, fmt.Errorf("为文件 %s 创建子任务: %w", entry.Asset.Filename, err)
		}
		prepared.Task.BatchID = batchID
		prepared.Task.BatchOrdinal = i + 1
		prepared.Task.BatchSize = len(entries)
		prepared.Task.SourceAssetID = entry.Asset.ID
		prepared.Task.SourceFilename = entry.Asset.Filename
		executions = append(executions, prepared)
	}
	if err := s.definitions.repo.CreateTaskExecutions(ctx, executions); err != nil {
		return nil, err
	}
	views := make([]TaskView, len(executions))
	for i := range executions {
		views[i] = taskView(*executions[i].Task)
	}
	return &TaskBatchView{ID: batchID, Size: len(views), Tasks: views}, nil
}

func cloneResourceBindingInputs(source []ResourceBindingInput) []ResourceBindingInput {
	cloned := make([]ResourceBindingInput, len(source))
	copy(cloned, source)
	for i := range cloned {
		cloned[i].Boundary = append(json.RawMessage(nil), source[i].Boundary...)
	}
	return cloned
}
