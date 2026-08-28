package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"reqflow/internal/domain/model"
)

func TestDefinitionServiceCreatesTaskSnapshotAndStepRuns(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo)
	definition, err := service.Create(ctx, activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if definition.ID == "" || definition.DefinitionHash == "" {
		t.Fatalf("定义未持久化完整: %+v", definition)
	}

	task, err := service.CreateTask(ctx, CreateTaskInput{
		DefinitionID: definition.ID,
		Title:        "清洗第 42 批规格",
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "11111111-1111-1111-1111-111111111111"},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "22222222-2222-2222-2222-222222222222"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || task.DefinitionID != definition.ID || task.DefinitionSnapshot == "" {
		t.Fatalf("任务快照缺失: %+v", task)
	}
	if len(repo.steps) != 3 || repo.steps[0].StepID != "extract" || repo.steps[1].StepID != "refine" || repo.steps[2].StepID != "publish" {
		t.Fatalf("StepRun 未按 step_id 创建: %+v", repo.steps)
	}
	if repo.steps[0].Kind != model.StepKindLLMExtract || repo.steps[1].Kind != model.StepKindLLMExtract {
		t.Fatalf("同 Kind 步骤应同时存在: %+v", repo.steps)
	}
	if repo.steps[0].ConfigHash == "" || repo.steps[0].ConfigHash != repo.steps[1].ConfigHash {
		t.Fatalf("等价 config 应有稳定哈希: %+v", repo.steps)
	}
	for _, binding := range repo.bindings {
		if binding.Direction != model.ResourceInput || binding.TaskID != task.ID {
			t.Fatalf("资源绑定未固化为输入: %+v", binding)
		}
	}
}

func TestDefinitionServiceValidatesRequiredBindings(t *testing.T) {
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo)
	definition, err := service.Create(context.Background(), activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateTask(context.Background(), CreateTaskInput{
		DefinitionID: definition.ID,
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "asset-set"},
		},
	})
	if err == nil {
		t.Fatal("缺少 target 必填端口应拒绝")
	}
}

func activeDefinition() model.TaskDefinition {
	return model.TaskDefinition{
		Key: "product_spec_clean", Name: "产品规格清洗", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
			"target":    {ResourceType: model.ResourceDataset, Required: true},
		},
		OutputPorts: map[string]model.PortDefinition{
			"batch": {ResourceType: model.ResourceDatasetBatch},
		},
		OutputBindings: map[string]string{"batch": "$step.publish.batch"},
		Steps: []model.StepDefinition{
			{ID: "extract", Name: "初次抽取", Kind: model.StepKindLLMExtract,
				Inputs:  map[string]string{"documents": "$task.documents"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts},
				Config:  json.RawMessage(`{"profile":"p1"}`)},
			{ID: "refine", Name: "二次抽取", Kind: model.StepKindLLMExtract, DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]model.ResourceType{"approved": model.ResourceApprovedRecords},
				Config:  json.RawMessage(`{ "profile": "p1" }`)},
			{ID: "publish", Name: "提交", Kind: model.StepKindDataPublish, DependsOn: []string{"refine"},
				Inputs:  map[string]string{"records": "$step.refine.approved", "dataset": "$task.target"},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}},
		},
	}
}

type memoryOrchestratorRepo struct {
	definition *model.TaskDefinition
	snapshot   []byte
	task       *model.Task
	bindings   []model.TaskResourceBinding
	steps      []model.StepRun
}

func (r *memoryOrchestratorRepo) CreateTaskDefinition(_ context.Context, definition *model.TaskDefinition, snapshot []byte) error {
	definition.ID = "definition-1"
	clone := *definition
	r.definition = &clone
	r.snapshot = append([]byte(nil), snapshot...)
	return nil
}

func (r *memoryOrchestratorRepo) GetTaskDefinition(_ context.Context, id string) (*model.TaskDefinition, error) {
	if r.definition == nil || r.definition.ID != id {
		return nil, errors.New("not found")
	}
	clone := *r.definition
	return &clone, nil
}

func (r *memoryOrchestratorRepo) CreateTaskExecution(_ context.Context, task *model.Task, bindings []model.TaskResourceBinding, steps []model.StepRun) error {
	task.ID = "task-1"
	for i := range bindings {
		bindings[i].TaskID = task.ID
	}
	for i := range steps {
		steps[i].TaskID = task.ID
	}
	clone := *task
	r.task = &clone
	r.bindings = append([]model.TaskResourceBinding(nil), bindings...)
	r.steps = append([]model.StepRun(nil), steps...)
	return nil
}

func (r *memoryOrchestratorRepo) GetTaskResourceBindings(context.Context, string) ([]model.TaskResourceBinding, error) {
	return append([]model.TaskResourceBinding(nil), r.bindings...), nil
}

func (r *memoryOrchestratorRepo) GetStepRuns(context.Context, string) ([]model.StepRun, error) {
	return append([]model.StepRun(nil), r.steps...), nil
}
