package app

import (
	"sort"
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

// seededTaskTypeOf 判断是否为代码 seed 注册的任务类型（向导注册的类型不算）。
func seededTaskTypeOf(typ string) (TaskTypeDefinition, bool) {
	for _, d := range taskTypeDefinitions() {
		if d.Type == typ {
			return d, true
		}
	}
	return TaskTypeDefinition{}, false
}

// TaskTypes 全部已注册任务类型的聚合定义（effective；元数据目录/聚合 API 用）。
// seed 定义与向导扩展类型统一过 override 合并层。
func TaskTypes() []TaskTypeDefinition {
	base := append(taskTypeDefinitions(), currentExtensionTypes()...)
	for i := range base {
		base[i] = applyMetadataOverrides(base[i])
	}
	return base
}

// TaskTypeOf 按任务类型取聚合定义（effective）；未注册返回 false。
func TaskTypeOf(typ string) (TaskTypeDefinition, bool) {
	if d, ok := seededTaskTypeOf(typ); ok {
		return applyMetadataOverrides(d), true
	}
	for _, d := range currentExtensionTypes() {
		if d.Type == typ {
			return applyMetadataOverrides(d), true
		}
	}
	return TaskTypeDefinition{}, false
}

/* ---- override 合并层（seed ∪ override = effective）---- */

// profileOverride 装配描述的 DB 覆盖（指令头/示例；写入绑定是代码资产，不覆盖）。
type profileOverride struct {
	Role    string
	Example string
}

// metadataOverrides 进程内 effective 缓存：MetadataService 启动装载、写后整体刷新
// （写路径与加载器同进程收口，单测覆盖「写后立即读」防失效遗漏）。
// map 只整体替换、绝不原地修改——读侧持旧引用即持旧快照，无竞态。
var metadataOverrides struct {
	sync.RWMutex
	schemas   map[string]model.DatasetSchema // key = 数据集类型
	profiles  map[string]profileOverride     // key = 任务类型
	workflows map[string]model.Workflow      // key = 任务类型（M4 工作流定义外置）
	extDefs   []TaskTypeDefinition           // 向导注册的扩展类型（无 seed 基线，锚行 enabled 才装载）
}

// setMetadataOverrides 整体替换 override 视图（MetadataService.Reload 的落点）。
func setMetadataOverrides(schemas map[string]model.DatasetSchema, profiles map[string]profileOverride,
	workflows map[string]model.Workflow, extDefs []TaskTypeDefinition) {
	metadataOverrides.Lock()
	metadataOverrides.schemas = schemas
	metadataOverrides.profiles = profiles
	metadataOverrides.workflows = workflows
	metadataOverrides.extDefs = extDefs
	metadataOverrides.Unlock()
}

// currentMetadataOverrides 取当前 override 快照（nil map = 无覆盖，读侧恒安全）。
func currentMetadataOverrides() (map[string]model.DatasetSchema, map[string]profileOverride) {
	metadataOverrides.RLock()
	defer metadataOverrides.RUnlock()
	return metadataOverrides.schemas, metadataOverrides.profiles
}

// currentWorkflows / currentExtensionTypes 读侧快照入口。
func currentWorkflows() map[string]model.Workflow {
	metadataOverrides.RLock()
	defer metadataOverrides.RUnlock()
	return metadataOverrides.workflows
}

func currentExtensionTypes() []TaskTypeDefinition {
	metadataOverrides.RLock()
	defer metadataOverrides.RUnlock()
	return metadataOverrides.extDefs
}

// applyMetadataOverrides seed 定义叠加 DB 覆盖：schema 覆盖按数据集类型命中时，
// 聚合定义的 schema 构面与 profile 的 schema/写入绑定同步替换（同一事实源）；
// 工作流/装配描述按任务类型命中替换。向导扩展类型同样走本函数——其构件本就来自
// 同一组 override 行，重复应用结果幂等。
func applyMetadataOverrides(d TaskTypeDefinition) TaskTypeDefinition {
	schemas, profiles := currentMetadataOverrides()
	workflows := currentWorkflows()
	if sc, ok := schemas[d.DatasetType]; ok {
		d.Schema = func() model.DatasetSchema { return sc }
		d.Profile.Schema = d.Schema
		d.Profile.Write.Schema = sc
	}
	if wf, ok := workflows[d.Type]; ok {
		wf.Type = d.Type // Type 由注册表持有，payload 一律以路径为准
		d.Workflow = wf
	}
	if p, ok := profiles[d.Type]; ok {
		d.Profile.Role, d.Profile.Example = p.Role, p.Example
	}
	return d
}

// allDefinitionsOf seed + 向导扩展类型的全量聚合定义（查找兜底遍历用）。
func allDefinitionsOf() []TaskTypeDefinition {
	return append(taskTypeDefinitions(), currentExtensionTypes()...)
}

// effectiveSchemaOf 按数据集类型取 effective schema（override → 域层 seed → 注册表定义）。
// 查询/查重等按数据集类型工作的读侧统一走这里（M3 前直连 model.SchemaOf）。
func effectiveSchemaOf(datasetType string) (model.DatasetSchema, bool) {
	schemas, _ := currentMetadataOverrides()
	if sc, ok := schemas[datasetType]; ok {
		return sc, true
	}
	if sc, ok := model.SchemaOf(datasetType); ok {
		return sc, true
	}
	for _, d := range allDefinitionsOf() {
		if d.DatasetType == datasetType && d.Schema != nil {
			return d.Schema(), true
		}
	}
	return model.DatasetSchema{}, false
}

// schemaOverridden / profileOverridden / workflowOverridden source 判定（元数据目录徽标）。
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

func workflowOverridden(taskType string) bool {
	workflows := currentWorkflows()
	_, ok := workflows[taskType]
	return ok
}

// customTaskType 是否为向导注册的扩展类型（无内置基线：不支持「回退内置」，可停用）。
func customTaskType(taskType string) bool {
	_, seeded := seededTaskTypeOf(taskType)
	return !seeded
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

// effectiveSchemas 全部 effective schema 目录（seed ∪ 向导扩展定义 ∪ 覆盖层，
// 按 Type 去重——覆盖优先，与 effectiveSchemaOf 同优先级）。
// /datasets/schemas 端点与前端表格筛选用（M3 前是纯 seed 的 model.Schemas()）。
func effectiveSchemas() []model.DatasetSchema {
	out := map[string]model.DatasetSchema{}
	for _, sc := range model.Schemas() {
		out[sc.Type] = sc
	}
	for _, d := range allDefinitionsOf() {
		if d.Schema != nil {
			sc := d.Schema()
			out[sc.Type] = sc
		}
	}
	schemas, _ := currentMetadataOverrides()
	for t, sc := range schemas {
		out[t] = sc
	}
	types := make([]string, 0, len(out))
	for t := range out {
		types = append(types, t)
	}
	sort.Strings(types)
	res := make([]model.DatasetSchema, 0, len(out))
	for _, t := range types {
		res = append(res, out[t])
	}
	return res
}
