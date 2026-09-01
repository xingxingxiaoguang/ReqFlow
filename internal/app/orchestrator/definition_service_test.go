package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestDefinitionServiceCreatesTaskSnapshotAndStepRuns(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo, definitionTestRegistry(t), repo)
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
	if repo.steps[0].Kind != model.StepKindDocumentExtract || repo.steps[1].Kind != model.StepKindDocumentExtract {
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
	service := NewDefinitionService(repo, definitionTestRegistry(t), repo)
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

func TestDefinitionServiceArchivesAndRestoresDefinition(t *testing.T) {
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo, definitionTestRegistry(t), repo)
	definition, err := service.Create(context.Background(), activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ArchiveDefinition(context.Background(), definition.ID); err != nil {
		t.Fatal(err)
	}
	if repo.definition.Status != model.TaskDefinitionRetired {
		t.Fatalf("归档后状态 = %s", repo.definition.Status)
	}
	if _, err := service.CreateTask(context.Background(), CreateTaskInput{DefinitionID: definition.ID}); err == nil {
		t.Fatal("已归档流程不应允许创建任务")
	}
	if err := service.RestoreDefinition(context.Background(), definition.ID); err != nil {
		t.Fatal(err)
	}
	if repo.definition.Status != model.TaskDefinitionActive {
		t.Fatalf("恢复后状态 = %s", repo.definition.Status)
	}
}

func TestPrepareTaskMergesStepConfigOverrides(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo, definitionTestRegistry(t), repo)
	definition, err := service.Create(ctx, activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	task, err := service.CreateTask(ctx, CreateTaskInput{
		DefinitionID: definition.ID,
		Bindings: []model.TaskResourceBinding{
			{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "11111111-1111-1111-1111-111111111111"},
			{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "22222222-2222-2222-2222-222222222222"},
		},
		StepConfigs: map[string]json.RawMessage{"extract": json.RawMessage(`{"profile":"p2"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	configs := snapshotStepConfigs(t, task.DefinitionSnapshot)
	if string(configs["extract"]) != `{"profile":"p2"}` {
		t.Fatalf("extract 未合并任务级配置: %s", configs["extract"])
	}
	if string(configs["refine"]) != `{"profile":"p1"}` {
		t.Fatalf("未覆盖的步骤不应被改动: %s", configs["refine"])
	}
}

func TestPrepareTaskRejectsInvalidStepConfigOverrides(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	service := NewDefinitionService(repo, definitionTestRegistry(t), repo)
	definition, err := service.Create(ctx, activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	bindings := []model.TaskResourceBinding{
		{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "asset-set"},
		{PortName: "target", ResourceType: model.ResourceDataset, ResourceID: "dataset-1"},
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{DefinitionID: definition.ID, Bindings: bindings,
		StepConfigs: map[string]json.RawMessage{"publish": json.RawMessage(`{"x":1}`)}}); err == nil {
		t.Fatal("data.publish 不应允许任务级配置覆盖")
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{DefinitionID: definition.ID, Bindings: bindings,
		StepConfigs: map[string]json.RawMessage{"ghost": json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("不存在的步骤应被拒绝")
	}
}

func TestPrepareTaskUsesRuntimeValidationAfterMerge(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	registry, err := NewRegistry(strictRuntimeExecutor{testExecutor{kind: model.StepKindDocumentExtract}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewDefinitionService(repo, registry, repo)
	definition, err := service.Create(ctx, model.TaskDefinition{
		Key: "runtime_gate", Name: "运行门", Status: model.TaskDefinitionActive,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
		},
		Steps: []model.StepDefinition{{
			ID: "extract", Name: "抽取", Kind: model.StepKindDocumentExtract,
			Inputs:  map[string]string{"documents": "$task.documents"},
			Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts},
			Config:  json.RawMessage(`{}`),
		}},
	})
	if err != nil {
		t.Fatalf("定义级允许空配置占位: %v", err)
	}
	bindings := []model.TaskResourceBinding{
		{PortName: "documents", ResourceType: model.ResourceAssetSet, ResourceID: "asset-set"},
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{DefinitionID: definition.ID, Bindings: bindings}); err == nil {
		t.Fatal("未落实运行期配置的任务应被拒绝")
	}
	if _, err := service.CreateTask(ctx, CreateTaskInput{DefinitionID: definition.ID, Bindings: bindings,
		StepConfigs: map[string]json.RawMessage{"extract": json.RawMessage(`{"profile":"p1"}`)}}); err != nil {
		t.Fatalf("落实配置后应通过: %v", err)
	}
}

func TestEnsureBuiltinDefinitionsSeedsIdempotently(t *testing.T) {
	ctx := context.Background()
	repo := &memoryOrchestratorRepo{}
	registry, err := NewRegistry(
		testExecutor{kind: model.StepKindSourceParse},
		testExecutor{kind: model.StepKindDocumentExtract},
		testExecutor{kind: model.StepKindDataTransform},
		testExecutor{kind: model.StepKindDataValidate},
		testExecutor{kind: model.StepKindDataPublish},
		testExecutor{kind: model.StepKindDataQueryDerive},
		testExecutor{kind: model.StepKindRetrievalBuild},
		testExecutor{kind: model.StepKindKnowledgeAnalyze},
		testExecutor{kind: model.StepKindAnalysisPublish},
		testExecutor{kind: model.StepKindArtifactRender},
		testExecutor{kind: model.StepKindGraphBuild},
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewDefinitionService(repo, registry, repo)
	created, err := service.EnsureBuiltinDefinitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("应种子两个固定流程: %v", created)
	}
	if _, ok := repo.defsByKey["default|"+BuiltinCleaningDefinitionKey]; !ok {
		t.Fatal("缺少数据清洗固定流程")
	}
	if _, ok := repo.defsByKey["default|"+BuiltinIndexDefinitionKey]; !ok {
		t.Fatal("缺少索引建立固定流程")
	}
	again, err := service.EnsureBuiltinDefinitions(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("重复种子应为空操作: created=%v err=%v", again, err)
	}
}

func snapshotStepConfigs(t *testing.T, snapshot string) map[string]string {
	t.Helper()
	var definition struct {
		Steps []struct {
			ID     string          `json:"id"`
			Config json.RawMessage `json:"config"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(snapshot), &definition); err != nil {
		t.Fatalf("解析定义快照: %v", err)
	}
	out := map[string]string{}
	for _, step := range definition.Steps {
		out[step.ID] = string(step.Config)
	}
	return out
}

func definitionTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(
		testExecutor{kind: model.StepKindDocumentExtract},
		testExecutor{kind: model.StepKindDataPublish},
	)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

type testExecutor struct{ kind model.StepKind }

func (e testExecutor) Kind() model.StepKind                                         { return e.kind }
func (testExecutor) ValidateDefinition(context.Context, model.StepDefinition) error { return nil }
func (testExecutor) Execute(context.Context, StepRunContext) (StepResult, error) {
	return StepResult{}, nil
}
func (testExecutor) Resume(context.Context, StepRunContext, json.RawMessage) (StepResult, error) {
	return StepResult{}, nil
}

// strictRuntimeExecutor 模拟「定义级占位、任务级必须落实」的执行器合同。
type strictRuntimeExecutor struct{ testExecutor }

func (strictRuntimeExecutor) ValidateRuntimeStep(_ context.Context, step model.StepDefinition) error {
	if len(step.Config) == 0 || string(step.Config) == "{}" || string(step.Config) == "null" {
		return fmt.Errorf("步骤 %s 的运行期配置未落实", step.ID)
	}
	return nil
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
			{ID: "extract", Name: "初次抽取", Kind: model.StepKindDocumentExtract,
				Inputs:  map[string]string{"documents": "$task.documents"},
				Outputs: map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts},
				Config:  json.RawMessage(`{"profile":"p1"}`)},
			{ID: "refine", Name: "二次抽取", Kind: model.StepKindDocumentExtract, DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]model.ResourceType{"approved": model.ResourceApprovedRecords},
				Config:  json.RawMessage(`{ "profile": "p1" }`)},
			{ID: "publish", Name: "提交", Kind: model.StepKindDataPublish, DependsOn: []string{"refine"},
				Inputs:  map[string]string{"approved": "$step.refine.approved"},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch}},
		},
	}
}

type memoryOrchestratorRepo struct {
	definition *model.TaskDefinition
	defsByKey  map[string]*model.TaskDefinition
	snapshot   []byte
	task       *model.Task
	bindings   []model.TaskResourceBinding
	steps      []model.StepRun
	tasks      []*model.Task
	executions []port.TaskExecutionCreate
}

func (r *memoryOrchestratorRepo) CreateTaskDefinition(_ context.Context, definition *model.TaskDefinition, snapshot []byte) error {
	definition.ID = fmt.Sprintf("definition-%d", len(r.defsByKey)+1)
	clone := *definition
	r.definition = &clone
	if r.defsByKey == nil {
		r.defsByKey = map[string]*model.TaskDefinition{}
	}
	keyClone := clone
	r.defsByKey[definition.WorkspaceID+"|"+definition.Key] = &keyClone
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

func (r *memoryOrchestratorRepo) GetTaskDefinitionByKey(_ context.Context, workspaceID, key string) (*model.TaskDefinition, bool, error) {
	definition, ok := r.defsByKey[workspaceID+"|"+key]
	if !ok {
		return nil, false, nil
	}
	clone := *definition
	return &clone, true, nil
}

func (r *memoryOrchestratorRepo) SetTaskDefinitionStatus(_ context.Context, id, fromStatus, toStatus string) error {
	if r.definition == nil || r.definition.ID != id || r.definition.Status != fromStatus {
		return errors.New("invalid status transition")
	}
	r.definition.Status = toStatus
	return nil
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

func (r *memoryOrchestratorRepo) CreateTaskExecutions(_ context.Context, executions []port.TaskExecutionCreate) error {
	r.executions = append([]port.TaskExecutionCreate(nil), executions...)
	r.tasks = make([]*model.Task, len(executions))
	for i := range executions {
		executions[i].Task.ID = fmt.Sprintf("task-%d", i+1)
		clone := *executions[i].Task
		r.tasks[i] = &clone
		for j := range executions[i].Bindings {
			executions[i].Bindings[j].TaskID = clone.ID
		}
		for j := range executions[i].Steps {
			executions[i].Steps[j].TaskID = clone.ID
		}
	}
	return nil
}

func (r *memoryOrchestratorRepo) GetTaskResourceBindings(context.Context, string) ([]model.TaskResourceBinding, error) {
	return append([]model.TaskResourceBinding(nil), r.bindings...), nil
}

func (r *memoryOrchestratorRepo) GetStepRuns(context.Context, string) ([]model.StepRun, error) {
	return append([]model.StepRun(nil), r.steps...), nil
}

func (*memoryOrchestratorRepo) ResolveTaskResource(_ context.Context, _ string, binding model.TaskResourceBinding, _ string) (model.TaskResourceBinding, error) {
	return binding, nil
}
