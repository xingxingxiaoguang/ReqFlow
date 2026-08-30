package platformagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"

	baseagent "reqflow/internal/app/agent"
	appcatalog "reqflow/internal/app/catalog"
	apporchestrator "reqflow/internal/app/orchestrator"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func buildTools(deps Dependencies, workspaceID string) []baseagent.Tool {
	base := platformTool{deps: deps, workspaceID: workspaceID}
	return []baseagent.Tool{
		&listWorkflowsTool{base}, &createWorkflowTool{base},
		&listTasksTool{base}, &createTaskTool{base}, &runTaskTool{base},
		&queryDataTool{base}, &indexDatasetTool{base},
	}
}

type platformTool struct {
	deps        Dependencies
	workspaceID string
}

func toolError(err error) baseagent.ToolOutput {
	return baseagent.ToolOutput{Output: err.Error(), Details: err.Error(), IsError: true}
}

func toolJSON(value any, details string) baseagent.ToolOutput {
	raw, err := json.Marshal(value)
	if err != nil {
		return toolError(err)
	}
	return baseagent.ToolOutput{Output: string(raw), Details: details}
}

func decodeStrict(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("参数非法: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("参数只能包含一个 JSON object")
	}
	return nil
}

/* ---- 流程工具 ---- */

type listWorkflowsTool struct{ platformTool }

func (*listWorkflowsTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "list_workflows", Description: "查询 ReqFlow 流程定义，可按草稿或已发布状态过滤。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["draft","active","retired"]},"limit":{"type":"integer","minimum":1,"maximum":200}},"additionalProperties":false}`)}
}

func (t *listWorkflowsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var args struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return toolError(err)
	}
	definitions, err := t.deps.Catalog.ListDefinitions(ctx, appcatalog.Query{
		WorkspaceID: t.workspaceID, Status: args.Status, Limit: args.Limit,
	})
	if err != nil {
		return toolError(err)
	}
	type stepSummary struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	type workflowSummary struct {
		ID          string                          `json:"id"`
		Key         string                          `json:"key"`
		Name        string                          `json:"name"`
		Description string                          `json:"description,omitempty"`
		Status      string                          `json:"status"`
		InputPorts  map[string]model.PortDefinition `json:"input_ports"`
		Steps       []stepSummary                   `json:"steps"`
	}
	items := make([]workflowSummary, len(definitions))
	for i, definition := range definitions {
		steps := make([]stepSummary, len(definition.Steps))
		for j, step := range definition.Steps {
			steps[j] = stepSummary{ID: step.ID, Name: step.Name, Kind: string(step.Kind)}
		}
		items[i] = workflowSummary{ID: definition.ID, Key: definition.Key, Name: definition.Name,
			Description: definition.Description, Status: definition.Status,
			InputPorts: definition.InputPorts, Steps: steps}
	}
	return toolJSON(map[string]any{"workflows": items}, fmt.Sprintf("查到 %d 个流程", len(items)))
}

func (*listWorkflowsTool) PromptSnippet() string {
	return "list_workflows：查询草稿或已发布流程，以及输入端口和步骤概况"
}
func (*listWorkflowsTool) PromptGuidelines() []string {
	return []string{"创建任务前先用 list_workflows 获取真实 definition_id 和必填输入端口"}
}

type createWorkflowTool struct{ platformTool }

func (*createWorkflowTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "create_workflow", Description: "创建一个经过 DAG、端口衔接和执行器配置校验的 ReqFlow 流程。",
		Parameters: json.RawMessage(`{
  "type":"object",
  "properties":{
    "key":{"type":"string","pattern":"^[a-z][a-z0-9_]*$"},
    "name":{"type":"string"},
    "description":{"type":"string"},
    "status":{"type":"string","enum":["draft","active"]},
    "input_ports":{"type":"object","additionalProperties":{"type":"object","properties":{"resource_type":{"type":"string"},"required":{"type":"boolean"},"description":{"type":"string"}},"required":["resource_type"],"additionalProperties":false}},
    "output_ports":{"type":"object","additionalProperties":{"type":"object","properties":{"resource_type":{"type":"string"},"required":{"type":"boolean"},"description":{"type":"string"}},"required":["resource_type"],"additionalProperties":false}},
    "output_bindings":{"type":"object","additionalProperties":{"type":"string"}},
    "steps":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"kind":{"type":"string"},"depends_on":{"type":"array","items":{"type":"string"}},"inputs":{"type":"object","additionalProperties":{"type":"string"}},"outputs":{"type":"object","additionalProperties":{"type":"string"}},"config":{"type":"object"}},"required":["id","name","kind"],"additionalProperties":false}}
  },
  "required":["key","name","status","input_ports","steps"],
  "additionalProperties":false
}`)}
}

func (t *createWorkflowTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var input apporchestrator.CreateDefinitionInput
	if err := decodeStrict(call.Arguments, &input); err != nil {
		return toolError(err)
	}
	input.WorkspaceID = t.workspaceID
	definition, err := t.deps.Definitions.Register(ctx, input)
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"workflow": definition},
		fmt.Sprintf("已创建流程「%s」（%s）", definition.Name, definition.Status))
}

func (*createWorkflowTool) PromptSnippet() string {
	return "create_workflow：创建带步骤依赖、资源端口和内嵌规则配置的流程"
}
func (*createWorkflowTool) PromptGuidelines() []string {
	return []string{
		"只有用户明确要求创建流程时才调用；想法讨论不要落库",
		"步骤输入只能引用 $task.<port> 或依赖祖先的 $step.<step>.<port>；发布态 active 会执行完整执行器校验",
		"不确定某个 profile/schema/resource ID 时先查询，不得猜测 UUID",
	}
}

/* ---- 任务工具 ---- */

type listTasksTool struct{ platformTool }

func (*listTasksTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "list_tasks", Description: "查询 ReqFlow 业务任务及其当前执行状态。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":["pending","running","pausing","awaiting","paused","succeeded","failed"]},"limit":{"type":"integer","minimum":1,"maximum":200}},"additionalProperties":false}`)}
}

func (t *listTasksTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var args struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return toolError(err)
	}
	tasks, err := t.deps.Tasks.ListViews(ctx, apporchestrator.TaskQuery{
		WorkspaceID: t.workspaceID, Status: args.Status, Limit: args.Limit,
	})
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"tasks": tasks}, fmt.Sprintf("查到 %d 个任务", len(tasks)))
}

func (*listTasksTool) PromptSnippet() string { return "list_tasks：查询任务列表和执行状态" }
func (*listTasksTool) PromptGuidelines() []string {
	return []string{"运行、追问或判断任务结果前先取得真实 task_id 和最新状态"}
}

type createTaskTool struct{ platformTool }

func (*createTaskTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "create_task", Description: "从已发布流程创建任务，并把流程输入端口绑定到具体平台资源。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"definition_id":{"type":"string"},"title":{"type":"string"},"bindings":{"type":"array","items":{"type":"object","properties":{"port_name":{"type":"string"},"resource_type":{"type":"string"},"resource_id":{"type":"string"},"resource_alias":{"type":"string"},"boundary":{"type":"object"}},"required":["port_name","resource_type"],"anyOf":[{"required":["resource_id"]},{"required":["resource_alias"]}],"additionalProperties":false}}},"required":["definition_id","bindings"],"additionalProperties":false}`)}
}

func (t *createTaskTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var input apporchestrator.CreateExecutionInput
	if err := decodeStrict(call.Arguments, &input); err != nil {
		return toolError(err)
	}
	task, err := t.deps.Definitions.CreateExecution(ctx, input)
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"task": task, "started": false},
		fmt.Sprintf("已创建任务「%s」，尚未运行", task.Title))
}

func (*createTaskTool) PromptSnippet() string {
	return "create_task：从已发布流程和资源绑定创建任务"
}
func (*createTaskTool) PromptGuidelines() []string {
	return []string{
		"先查流程输入端口，再为每个 required 端口提供相同 resource_type 的真实资源",
		"创建任务不会自动运行；用户要求立即执行时随后调用 run_task",
	}
}

type runTaskTool struct{ platformTool }

func (*runTaskTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "run_task", Description: "启动一个 pending 状态的 ReqFlow 任务，并返回启动后的完整任务快照。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"task_id":{"type":"string"}},"required":["task_id"],"additionalProperties":false}`)}
}

func (t *runTaskTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return toolError(err)
	}
	if err := t.deps.Runtime.Start(ctx, strings.TrimSpace(args.TaskID)); err != nil {
		return toolError(err)
	}
	snapshot, err := t.deps.Runtime.Snapshot(ctx, strings.TrimSpace(args.TaskID))
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"task": snapshot},
		fmt.Sprintf("已启动任务「%s」", snapshot.Task.Title))
}

func (*runTaskTool) PromptSnippet() string {
	return "run_task：启动任务并读取启动后的步骤快照"
}
func (*runTaskTool) PromptGuidelines() []string {
	return []string{"只有用户明确要求执行时才启动任务；已运行或终态任务不要重复启动"}
}

/* ---- 数据工具 ---- */

type queryDataTool struct{ platformTool }

func (*queryDataTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "query_data", Description: "查询数据集与可用索引，或在指定数据集的激活快照上执行关键词、语义或混合检索。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"dataset_id":{"type":"string"},"retrieval_snapshot_id":{"type":"string"},"query":{"type":"string"},"filters":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},"mode":{"type":"string","enum":["lexical","semantic","hybrid"]},"top_k":{"type":"integer","minimum":1,"maximum":50},"rerank_enabled":{"type":"boolean"}},"additionalProperties":false}`)}
}

func (t *queryDataTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var args struct {
		DatasetID  string              `json:"dataset_id"`
		SnapshotID string              `json:"retrieval_snapshot_id"`
		Query      string              `json:"query"`
		Filters    map[string][]string `json:"filters"`
		Mode       string              `json:"mode"`
		TopK       int                 `json:"top_k"`
		Rerank     bool                `json:"rerank_enabled"`
	}
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return toolError(err)
	}
	args.DatasetID, args.SnapshotID, args.Query = strings.TrimSpace(args.DatasetID),
		strings.TrimSpace(args.SnapshotID), strings.TrimSpace(args.Query)
	if args.Query == "" {
		datasets, err := t.deps.Catalog.ListDatasets(ctx, appcatalog.Query{
			WorkspaceID: t.workspaceID, Status: model.DatasetStatusActive, Limit: 100,
		})
		if err != nil {
			return toolError(err)
		}
		if args.DatasetID != "" {
			datasets = filterDatasets(datasets, args.DatasetID)
		}
		snapshots, err := t.deps.Retrieval.ListSnapshotViews(ctx, args.DatasetID, "", model.RetrievalSnapshotActive, 100)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(map[string]any{"datasets": datasets, "active_snapshots": snapshots},
			fmt.Sprintf("查到 %d 个数据集、%d 个可查询索引", len(datasets), len(snapshots)))
	}
	if args.SnapshotID == "" {
		if args.DatasetID == "" {
			return toolError(fmt.Errorf("执行数据检索前必须提供 dataset_id 或 retrieval_snapshot_id；可先不传 query 查询可用数据集"))
		}
		snapshots, err := t.deps.Retrieval.ListSnapshotViews(ctx, args.DatasetID, "", model.RetrievalSnapshotActive, 20)
		if err != nil {
			return toolError(err)
		}
		if len(snapshots) == 0 {
			return toolError(fmt.Errorf("数据集 %s 没有激活索引，可先调用 index_dataset", args.DatasetID))
		}
		args.SnapshotID = snapshots[0].ID
	}
	if args.TopK <= 0 {
		args.TopK = 8
	}
	if args.Mode == "" {
		args.Mode = string(model.RetrievalModeHybrid)
	}
	strategy := model.RetrievalSearchStrategy{Mode: model.RetrievalSearchMode(args.Mode),
		RecallLimit: max(args.TopK*4, 20), TopK: args.TopK, RerankEnabled: args.Rerank,
		RerankTopN: max(args.TopK*2, 20)}
	switch strategy.Mode {
	case model.RetrievalModeLexical:
		strategy.LexicalWeight = 1
	case model.RetrievalModeSemantic:
		strategy.SemanticWeight = 1
	default:
		strategy.LexicalWeight, strategy.SemanticWeight = 1, 1
	}
	result, err := t.deps.Retrieval.SearchAPI(ctx, appretrieval.SearchAPIRequest{
		RetrievalSnapshotID: args.SnapshotID, Query: args.Query, Filters: args.Filters, Strategy: strategy,
	})
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"search": result},
		fmt.Sprintf("数据检索命中 %d 条，耗时 %dms", len(result.Hits), result.TookMS))
}

func (*queryDataTool) PromptSnippet() string {
	return "query_data：发现数据集/激活索引，或执行关键词、语义、混合检索"
}
func (*queryDataTool) PromptGuidelines() []string {
	return []string{
		"首次查询或不知道 dataset_id 时先以空 query 调用，查看数据集和 active snapshot",
		"精确编号和专有名词用 lexical，自然语言含义用 semantic，一般问题默认 hybrid",
		"回答数据问题时引用命中的 dataset_item_id、fields 与 provenance，不得超出证据推断",
	}
}

type indexDatasetTool struct{ platformTool }

func (*indexDatasetTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "index_dataset", Description: "为数据集选择匹配的索引规则，创建并启动 retrieval.build 任务；需要时就地创建最小索引流程。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"dataset_id":{"type":"string"},"retrieval_profile_id":{"type":"string"},"definition_id":{"type":"string"},"title":{"type":"string"}},"required":["dataset_id"],"additionalProperties":false}`)}
}

type indexCandidate struct {
	DefinitionID string
	PortName     string
	ProfileID    string
}

func (t *indexDatasetTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) baseagent.ToolOutput {
	var args struct {
		DatasetID    string `json:"dataset_id"`
		ProfileID    string `json:"retrieval_profile_id"`
		DefinitionID string `json:"definition_id"`
		Title        string `json:"title"`
	}
	if err := decodeStrict(call.Arguments, &args); err != nil {
		return toolError(err)
	}
	args.DatasetID, args.ProfileID, args.DefinitionID = strings.TrimSpace(args.DatasetID),
		strings.TrimSpace(args.ProfileID), strings.TrimSpace(args.DefinitionID)
	datasets, err := t.deps.Catalog.ListDatasets(ctx, appcatalog.Query{
		WorkspaceID: t.workspaceID, Status: model.DatasetStatusActive, Limit: 200,
	})
	if err != nil {
		return toolError(err)
	}
	var dataset *appcatalog.DatasetView
	for i := range datasets {
		if datasets[i].ID == args.DatasetID {
			dataset = &datasets[i]
			break
		}
	}
	if dataset == nil {
		return toolError(fmt.Errorf("活动数据集不存在: %s", args.DatasetID))
	}
	profiles, err := t.deps.Retrieval.ListProfileViews(ctx, t.workspaceID, dataset.SchemaID, 100)
	if err != nil {
		return toolError(err)
	}
	profileIDs := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		profileIDs[profile.ID] = true
	}
	if args.ProfileID != "" && !profileIDs[args.ProfileID] {
		return toolError(fmt.Errorf("索引规则 %s 不属于数据集 schema；可选规则: %s", args.ProfileID, profileChoices(profiles)))
	}

	definitions, err := t.deps.Catalog.ListDefinitions(ctx, appcatalog.Query{
		WorkspaceID: t.workspaceID, Status: model.TaskDefinitionActive, Limit: 200,
	})
	if err != nil {
		return toolError(err)
	}
	candidates := indexCandidates(definitions, profileIDs)
	var selected *indexCandidate
	for i := range candidates {
		candidate := &candidates[i]
		if args.DefinitionID != "" && candidate.DefinitionID != args.DefinitionID {
			continue
		}
		if args.ProfileID != "" && candidate.ProfileID != args.ProfileID {
			continue
		}
		selected = candidate
		break
	}
	if args.DefinitionID != "" && selected == nil {
		return toolError(fmt.Errorf("流程 %s 不是该数据集可用的单输入索引流程", args.DefinitionID))
	}

	createdDefinition := false
	if selected == nil {
		profileID := args.ProfileID
		if profileID == "" {
			if len(profiles) == 0 {
				return toolError(fmt.Errorf("数据集「%s」尚无索引规则，请先在数据管理的 schema 字段上创建索引规则", dataset.Name))
			}
			if len(profiles) > 1 {
				return toolError(fmt.Errorf("数据集有多个索引规则，请指定 retrieval_profile_id：%s", profileChoices(profiles)))
			}
			profileID = profiles[0].ID
		}
		definition, createErr := t.createIndexDefinition(ctx, dataset, profileID)
		if createErr != nil {
			return toolError(createErr)
		}
		createdDefinition = true
		selected = &indexCandidate{DefinitionID: definition.ID, PortName: "dataset", ProfileID: profileID}
	}

	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = "为「" + dataset.Name + "」建立检索索引"
	}
	task, err := t.deps.Definitions.CreateExecution(ctx, apporchestrator.CreateExecutionInput{
		DefinitionID: selected.DefinitionID, Title: title,
		Bindings: []apporchestrator.ResourceBindingInput{{PortName: selected.PortName,
			ResourceType: string(model.ResourceDatasetBoundary), ResourceID: dataset.ID}},
	})
	if err != nil {
		return toolError(err)
	}
	if err := t.deps.Runtime.Start(ctx, task.ID); err != nil {
		return toolError(fmt.Errorf("索引任务已创建但启动失败（task_id=%s）: %w", task.ID, err))
	}
	snapshot, err := t.deps.Runtime.Snapshot(ctx, task.ID)
	if err != nil {
		return toolError(err)
	}
	return toolJSON(map[string]any{"task": snapshot, "workflow_created": createdDefinition,
		"retrieval_profile_id": selected.ProfileID}, fmt.Sprintf("已为「%s」启动索引任务", dataset.Name))
}

func (t *indexDatasetTool) createIndexDefinition(ctx context.Context, dataset *appcatalog.DatasetView,
	profileID string) (*apporchestrator.DefinitionView, error) {
	key := "index_dataset_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	config, _ := json.Marshal(map[string]string{"retrieval_profile_id": profileID})
	return t.deps.Definitions.Register(ctx, apporchestrator.CreateDefinitionInput{
		WorkspaceID: t.workspaceID, Key: key, Name: "为「" + dataset.Name + "」建立检索索引",
		Description: "由 ReqFlow Agent 按需创建的单步骤索引流程", Status: model.TaskDefinitionActive,
		InputPorts: map[string]apporchestrator.PortInput{"dataset": {
			ResourceType: string(model.ResourceDatasetBoundary), Required: true, Description: "待建立索引的数据集边界",
		}},
		OutputPorts: map[string]apporchestrator.PortInput{"snapshot": {
			ResourceType: string(model.ResourceRetrievalSnapshot), Description: "激活后的检索快照",
		}},
		OutputBindings: map[string]string{"snapshot": "$step.build_index.snapshot"},
		Steps: []apporchestrator.StepDefinitionInput{{ID: "build_index", Name: "建立检索索引",
			Kind: string(model.StepKindRetrievalBuild), Inputs: map[string]string{"dataset": "$task.dataset"},
			Outputs: map[string]string{"snapshot": string(model.ResourceRetrievalSnapshot)}, Config: config}},
	})
}

func (*indexDatasetTool) PromptSnippet() string {
	return "index_dataset：选择索引规则，为数据集创建并启动索引任务，必要时就地创建流程"
}
func (*indexDatasetTool) PromptGuidelines() []string {
	return []string{
		"建立索引前先用 query_data 确认真实 dataset_id 以及是否已有 active snapshot",
		"多个索引规则时把工具返回的选择交给用户；只有一个规则时可直接执行",
		"索引永远经由 retrieval.build 流程任务运行，不绕过编排器直接写快照",
	}
}

func filterDatasets(items []appcatalog.DatasetView, id string) []appcatalog.DatasetView {
	out := make([]appcatalog.DatasetView, 0, 1)
	for _, item := range items {
		if item.ID == id {
			out = append(out, item)
		}
	}
	return out
}

func profileChoices(profiles []appretrieval.ProfileView) string {
	choices := make([]string, len(profiles))
	for i, profile := range profiles {
		choices[i] = fmt.Sprintf("%s(%s)", profile.Name, profile.ID)
	}
	sort.Strings(choices)
	return strings.Join(choices, "、")
}

func indexCandidates(definitions []model.TaskDefinition, allowedProfiles map[string]bool) []indexCandidate {
	var candidates []indexCandidate
	for _, definition := range definitions {
		for _, step := range definition.Steps {
			if step.Kind != model.StepKindRetrievalBuild {
				continue
			}
			ref := strings.TrimPrefix(step.Inputs["dataset"], "$task.")
			input, ok := definition.InputPorts[ref]
			if !ok || step.Inputs["dataset"] != "$task."+ref || input.ResourceType != model.ResourceDatasetBoundary {
				continue
			}
			usable := true
			for name, portDefinition := range definition.InputPorts {
				if portDefinition.Required && name != ref {
					usable = false
				}
			}
			if !usable {
				continue
			}
			var config struct {
				ProfileID string `json:"retrieval_profile_id"`
			}
			if json.Unmarshal(step.Config, &config) != nil || !allowedProfiles[config.ProfileID] {
				continue
			}
			candidates = append(candidates, indexCandidate{DefinitionID: definition.ID,
				PortName: ref, ProfileID: config.ProfileID})
		}
	}
	return candidates
}
