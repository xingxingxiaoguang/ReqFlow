package app

import (
	"reqflow/internal/domain/model"
)

// 任务类型聚合注册表：一个任务类型的全部定义构件收敛为一处声明——
// 工作流（步骤链）+ 产出数据集类型 + 产出 schema + agent 装配描述。
// 新增任务类型 = 在 taskTypeDefinitions 加一条（必要时配套新增 StepKind 执行器）；
// 旧查找入口（WorkflowOf / AnalyzeProfileOf / datasetWritePlanFor）均委托于此，
// 元数据目录（metadata.go）与 /api/metadata 以本注册表为唯一事实源。
// 分层真相源与受控编辑见 docs/METADATA.md（M3 起本表叠加 DB override）。

// TaskTypeDefinition 一个任务类型的聚合定义（注册表的注册单元）。
type TaskTypeDefinition struct {
	Type        string                     // 任务类型标识（tasks.type）
	Workflow    model.Workflow             // 步骤链 + 依赖声明（创建任务时快照进 tasks.workflow）
	DatasetType string                     // 产出数据集类型（task↔dataset 接缝映射）
	Schema      func() model.DatasetSchema // 产出字段合同（提示词渲染/写入校验/条目身份的单一事实源）
	Profile     AnalyzeProfile             // agent 装配描述（指令头/示例/写入绑定）
}

// extraTaskTypes 测试注入的扩展任务类型（生产恒空）。金标准用例经此注册玩具
// 类型验证「零 struct/Normalize/ValuesOf 接入」（schema 端到端生效的定义）。
var extraTaskTypes []TaskTypeDefinition

// taskTypeDefinitions 全部已注册任务类型（唯一注册点；定义函数在各所属文件维护）。
func taskTypeDefinitions() []TaskTypeDefinition {
	all := []TaskTypeDefinition{
		{
			Type:        model.TaskTypeRequirementImport,
			Workflow:    requirementImportWorkflow(),
			DatasetType: model.DatasetTypeRequirement,
			Schema:      model.RequirementSchema,
			Profile:     requirementProfile(),
		},
	}
	return append(all, extraTaskTypes...)
}

// TaskTypes 全部已注册任务类型的聚合定义（元数据目录/聚合 API 用）。
func TaskTypes() []TaskTypeDefinition {
	return taskTypeDefinitions()
}

// TaskTypeOf 按任务类型取聚合定义；未注册返回 false。
func TaskTypeOf(typ string) (TaskTypeDefinition, bool) {
	for _, d := range taskTypeDefinitions() {
		if d.Type == typ {
			return d, true
		}
	}
	return TaskTypeDefinition{}, false
}
