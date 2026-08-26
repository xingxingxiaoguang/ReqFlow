package app

import (
	"encoding/json"

	"reqflow/internal/domain/model"
)

// 工作流注册表：内置任务类型的工作流定义（半元数据驱动——定义即数据，
// 执行引擎按 Step.Kind 查找注册的执行器）。新增任务类型 = 在此加一条定义
// + 复用/新增 kind 执行器（runner.go），不再动执行骨架。
// 创建任务时定义被快照进 tasks.workflow：任务自描述，且不受定义演进影响
// （旧任务按自己的快照执行与展示）。

// Workflows 全部内置工作流（目录：任务类型创建入口展示用）。
func Workflows() []model.Workflow {
	return []model.Workflow{requirementImportWorkflow()}
}

// WorkflowOf 按类型取工作流定义；未注册返回 false。
func WorkflowOf(typ string) (model.Workflow, bool) {
	for _, w := range Workflows() {
		if w.Type == typ {
			return w, true
		}
	}
	return model.Workflow{}, false
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
		Desc: "上传需求文档 → AI 拆解为结构化需求（agent 可自主查证已有数据集）→ 查重确认 → 生成需求数据集（后续任务的输入底料）",
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
					{Data: "parsed_text + special_requirements", Tool: "agent_loop（search_requirements / list_recent_requirements / search_datasets）"},
				},
			},
			{
				Seq: 4, Name: "生成数据集", Kind: model.StepKindDataset,
				Deps: []model.StepDependency{
					{Data: "items（上一步产物）+ dataset_name（人工命名）", Tool: "match（精确+语义两层查重）→ dataset_writer（embedder 向量化写入）"},
				},
			},
		},
	}
}
