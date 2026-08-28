package app

import (
	"encoding/json"

	"reqflow/internal/domain/model"
)

// 工作流定义（半元数据驱动——定义即数据，执行引擎按 Step.Kind 查找注册的执行器）。
// 注册点已收敛进聚合注册表（registry.go 的 taskTypeDefinitions）：新增任务类型
// = 注册表加一条聚合定义 + 复用/新增 kind 执行器（runner.go），不再动执行骨架。
// 创建任务时定义被快照进 tasks.workflow：任务自描述，且不受定义演进影响
// （旧任务按自己的快照执行与展示）。本文件的查找入口为聚合注册表的薄委托。

// Workflows 全部内置工作流（目录：任务类型创建入口展示用；委托聚合注册表）。
func Workflows() []model.Workflow {
	defs := TaskTypes()
	out := make([]model.Workflow, 0, len(defs))
	for _, d := range defs {
		out = append(out, d.Workflow)
	}
	return out
}

// WorkflowOf 按类型取工作流定义；未注册返回 false（委托聚合注册表）。
func WorkflowOf(typ string) (model.Workflow, bool) {
	d, ok := TaskTypeOf(typ)
	if !ok {
		return model.Workflow{}, false
	}
	return d.Workflow, true
}

// MarshalWorkflow 序列化工作流定义（tasks.workflow 快照）。
func MarshalWorkflow(w model.Workflow) string {
	b, err := json.Marshal(w)
	if err != nil {
		return ""
	}
	return string(b)
}

// ParseWorkflow 反序列化任务自带的工作流快照；为空（旧任务）时回退注册表。
func ParseWorkflow(task *model.Task) model.Workflow {
	if task.Workflow != "" {
		var w model.Workflow
		if json.Unmarshal([]byte(task.Workflow), &w) == nil && len(w.Steps) > 0 {
			return w
		}
	}
	if w, ok := WorkflowOf(task.Type); ok {
		return w
	}
	return model.Workflow{Type: task.Type, Steps: []model.WorkflowStep{}}
}

func requirementImportWorkflow() model.Workflow {
	return model.Workflow{
		Type: model.TaskTypeRequirementImport,
		Name: "需求导入",
		Desc: "上传需求文档 → AI 拆解为结构化需求（agent 自主阅读文档并分批产出草稿，可向人工提问）→ 查重确认 → 生成需求数据集（后续任务的输入底料）",
		Steps: []model.WorkflowStep{
			{
				Seq: 1, Name: "上传解析", Kind: model.StepKindParse,
				Deps: []model.StepDependency{
					{Data: "file（docx/pdf/md/txt 上传）", Tool: "doc_parser"},
				},
			},
			{
				Seq: 2, Name: "确认解析", Kind: model.StepKindHuman,
				Deps: []model.StepDependency{
					{Data: "parsed_text（上一步产物）", Tool: "human（修订 + 提交）"},
				},
			},
			{
				Seq: 3, Name: "AI 分析", Kind: model.StepKindAnalyze,
				Deps: []model.StepDependency{
					{Data: "parsed_text + special_requirements", Tool: "agent_loop（read_document / search_document / write_work_items / ask_human）"},
				},
			},
			{
				Seq: 4, Name: "生成数据集", Kind: model.StepKindDataset,
				Deps: []model.StepDependency{
					{Data: "items（上一步产物）+ 写入策略（merge/upsert/replace，目标 = 创建任务时绑定的数据集）", Tool: "match（精确+语义两层查重）→ dataset_writer（embedder 向量化写入）"},
				},
			},
		},
	}
}
