package app

import (
	"sync"

	"reqflow/internal/domain/model"
)

// 任务类型聚合注册表：一个任务类型的全部定义构件收敛为一处声明——
// 工作流（步骤链）+ 产出数据集类型 + 产出 schema + agent 装配描述。
// 新增任务类型 = 在 taskTypeDefinitions 加一条（必要时配套新增 StepKind 执行器）；
// 旧查找入口（WorkflowOf / AnalyzeProfileOf / datasetWritePlanFor）均委托于此，
// 元数据目录（metadata.go）与 /api/metadata 以本注册表为唯一事实源。
//
// 分层真相源（METADATA §4.1）：taskTypeDefinitions 是 code seed；M3 起 DB 覆盖
// （metadata_registry）由 MetadataService 装载进本包的 override 层，TaskTypeOf /
// TaskTypes 返回 seed ∪ override 的 effective 视图——运行时仍是进程内调用，
// 签名不变，绝不经 HTTP 取元数据。

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

// TaskTypes 全部已注册任务类型的聚合定义（effective；元数据目录/聚合 API 用）。
func TaskTypes() []TaskTypeDefinition {
	base := taskTypeDefinitions()
	for i := range base {
		base[i] = applyMetadataOverrides(base[i])
	}
	return base
}

// TaskTypeOf 按任务类型取聚合定义（effective）；未注册返回 false。
func TaskTypeOf(typ string) (TaskTypeDefinition, bool) {
	for _, d := range taskTypeDefinitions() {
		if d.Type == typ {
			return applyMetadataOverrides(d), true
		}
	}
	return TaskTypeDefinition{}, false
}

/* ---- override 合并层（seed ∪ override = effective）---- */

// profileOverride 装配描述的 DB 覆盖（指令头/示例；写入绑定与工作流是代码资产，不覆盖）。
type profileOverride struct {
	Role    string
	Example string
}

// metadataOverrides 进程内 effective 缓存：MetadataService 启动装载、写后整体刷新
// （写路径与加载器同进程收口，单测覆盖「写后立即读」防失效遗漏）。
// map 只整体替换、绝不原地修改——读侧持旧引用即持旧快照，无竞态。
var metadataOverrides struct {
	sync.RWMutex
	schemas  map[string]model.DatasetSchema // key = 数据集类型
	profiles map[string]profileOverride     // key = 任务类型
}

// setMetadataOverrides 整体替换 override 视图（MetadataService.Reload 的落点）。
func setMetadataOverrides(schemas map[string]model.DatasetSchema, profiles map[string]profileOverride) {
	metadataOverrides.Lock()
	metadataOverrides.schemas, metadataOverrides.profiles = schemas, profiles
	metadataOverrides.Unlock()
}

// currentMetadataOverrides 取当前 override 快照（nil map = 无覆盖，读侧恒安全）。
func currentMetadataOverrides() (map[string]model.DatasetSchema, map[string]profileOverride) {
	metadataOverrides.RLock()
	defer metadataOverrides.RUnlock()
	return metadataOverrides.schemas, metadataOverrides.profiles
}

// applyMetadataOverrides seed 定义叠加 DB 覆盖：schema 覆盖按数据集类型命中时，
// 聚合定义的 schema 构面与 profile 的 schema/写入绑定同步替换（同一事实源）。
func applyMetadataOverrides(d TaskTypeDefinition) TaskTypeDefinition {
	schemas, profiles := currentMetadataOverrides()
	if sc, ok := schemas[d.DatasetType]; ok {
		d.Schema = func() model.DatasetSchema { return sc }
		d.Profile.Schema = d.Schema
		d.Profile.Write.Schema = sc
	}
	if p, ok := profiles[d.Type]; ok {
		d.Profile.Role, d.Profile.Example = p.Role, p.Example
	}
	return d
}

// effectiveSchemaOf 按数据集类型取 effective schema（override → 域层 seed → 注册表扩展类型）。
// 查询/查重等按数据集类型工作的读侧统一走这里（M3 前直连 model.SchemaOf）。
func effectiveSchemaOf(datasetType string) (model.DatasetSchema, bool) {
	schemas, _ := currentMetadataOverrides()
	if sc, ok := schemas[datasetType]; ok {
		return sc, true
	}
	if sc, ok := model.SchemaOf(datasetType); ok {
		return sc, true
	}
	for _, d := range taskTypeDefinitions() {
		if d.DatasetType == datasetType && d.Schema != nil {
			return d.Schema(), true
		}
	}
	return model.DatasetSchema{}, false
}

// schemaOverridden / profileOverridden source 判定（元数据目录徽标）。
func schemaOverridden(datasetType string) bool {
	schemas, _ := currentMetadataOverrides()
	_, ok := schemas[datasetType]
	return ok
}

func profileOverridden(taskType string) bool {
	_, profiles := currentMetadataOverrides()
	_, ok := profiles[taskType]
	return ok
}

// seedSchemaOf 按数据集类型取 seed schema（不含 override；版本基线与回退载荷用）。
func seedSchemaOf(datasetType string) (model.DatasetSchema, bool) {
	if sc, ok := model.SchemaOf(datasetType); ok {
		return sc, true
	}
	for _, d := range taskTypeDefinitions() {
		if d.DatasetType == datasetType && d.Schema != nil {
			return d.Schema(), true
		}
	}
	return model.DatasetSchema{}, false
}
