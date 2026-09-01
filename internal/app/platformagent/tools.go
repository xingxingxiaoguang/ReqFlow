package platformagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	baseagent "reqflow/internal/app/agent"
	appcatalog "reqflow/internal/app/catalog"
	apporchestrator "reqflow/internal/app/orchestrator"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func buildTools(deps Dependencies, workspaceID string, settings map[string]bool) []baseagent.Tool {
	base := platformTool{deps: deps, workspaceID: workspaceID}
	all := []baseagent.Tool{
		&platformGuideTool{base}, &listWorkflowsTool{base},
		&listTasksTool{base}, &queryDataTool{base},
	}
	enabled := make([]baseagent.Tool, 0, len(all))
	for _, tool := range all {
		value, configured := settings[tool.Spec().Name]
		if !configured || value {
			enabled = append(enabled, tool)
		}
	}
	return enabled
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

/* ---- 平台使用规则 ---- */

const platformGuideText = `ReqFlow 是无代码 AI 数据管线平台：流程定义负责「怎么做」，任务负责「这一次执行」，数据集负责跨任务衔接。AI 负责读取、抽取和起草，审核与发布由人完成。

## 核心对象
- 流程（TaskDefinition）：可复用的步骤编排。字段含 key（小写字母开头，仅小写字母/数字/下划线）、name、input_ports / output_ports（resource_type + required + description）与 steps；status 为 draft（草稿）、active（已发布）、retired（退役）。
- 任务（Task）：从已发布流程派生的一次执行实例。状态流转 pending → running →（awaiting 停在人工门 / pausing / paused）→ succeeded / failed。
- 数据集（Dataset）：由 data.publish 步骤产出的结构化资产，带 schema 字段定义；语义与混合检索依赖数据集处于激活状态的检索快照。

## 建立流程（流程设计页）
1. 把业务 SOP 拆成步骤：每步声明 id、name、kind、depends_on，依赖关系必须构成无环 DAG。
2. 可用步骤类型：source.parse、document.extract、data.transform、data.validate、data.publish、data.query_derive、retrieval.build、knowledge.analyze、data.analysis_publish、artifact.render、graph.build、human.review（人工确认门：花钱前、写库前、终稿前必须由人确认）。
3. 端口衔接规则：步骤 inputs 只能引用 $task.<输入端口名> 或依赖祖先步骤的 $step.<step_id>.<输出端口名>；相连端口的 resource_type 必须一致；流程输出端口通过 output_bindings 绑定到 $step.<step_id>.<输出端口名>。
4. 先保存为 draft，再发布为 active；发布时平台会执行 DAG、端口衔接与执行器配置的完整校验，未通过则不能发布。

## 创建与运行任务（任务页）
1. 只能从 active 流程创建任务。
2. 为每个 required 输入端口绑定同 resource_type 的真实平台资源（resource_id 或 resource_alias）。
3. 任务创建后是 pending，需要在任务页手动启动；awaiting 表示任务停在 human.review 人工门，人工确认后才会继续。

## 数据集与索引（数据管理页）
1. 数据集只能由流程的 data.publish 步骤产出，不能绕过流程直接写数据。
2. 建立检索索引分两步：先在数据集 schema 字段上创建索引规则（retrieval profile），再运行包含 retrieval.build 步骤的流程任务，生成并激活检索快照。
3. 所有查询与检索都发生在激活快照上；数据集没有 active snapshot 时无法语义检索。

## Agent 能力边界
- 本 Agent 是只读顾问：只能查询流程、任务、数据集与执行检索，没有创建、修改、运行、发布类工具。
- 用户要求创建流程/任务、运行任务或建立索引时，按对应章节把操作整理成一步步的页面操作指引，等用户在页面上完成后，再代为查询结果与状态。`

type platformGuideTool struct{ platformTool }

func (*platformGuideTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "platform_guide", Description: "获取 ReqFlow 平台使用规则说明：核心对象模型、建立流程、创建与运行任务、数据集与索引规则，以及本 Agent 的能力边界。指导用户搭建流程或使用平台前先调用本工具。",
		Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`)}
}

func (*platformGuideTool) Execute(_ context.Context, _ port.ToolCall, _ func(string)) baseagent.ToolOutput {
	return baseagent.ToolOutput{Output: platformGuideText, Details: "已取得平台使用规则"}
}

func (*platformGuideTool) PromptSnippet() string {
	return "platform_guide：获取平台使用规则，用于指导用户搭建流程、运行任务和管理数据集"
}

func (*platformGuideTool) PromptGuidelines() []string {
	return []string{
		"指导用户创建流程、任务或建立索引前，先调用 platform_guide 取得平台规则，再按规则分步指引",
		"指引中的步骤类型、端口引用和状态名必须与 platform_guide 返回的规则一致",
	}
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
	return []string{"设计流程指引前先用 list_workflows 了解平台已有流程，避免给出重复建议"}
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
	return []string{"回答任务进展或判断下一步建议前，先取得真实 task_id 和最新状态"}
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
			return toolError(fmt.Errorf("数据集 %s 没有激活索引，可按平台使用规则指导用户建立索引", args.DatasetID))
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

func filterDatasets(items []appcatalog.DatasetView, id string) []appcatalog.DatasetView {
	out := make([]appcatalog.DatasetView, 0, 1)
	for _, item := range items {
		if item.ID == id {
			out = append(out, item)
		}
	}
	return out
}
