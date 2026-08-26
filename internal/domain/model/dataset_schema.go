// 数据集 schema（类型化字段的声明式合同）。
// 写入校验、向量文档组装、表格渲染、条目身份（item_key）全部由此驱动——
// 新任务类型接入数据集只需注册 schema，不改 Writer/仓储/前端表格。
package model

// FieldType 字段值类型（筛选器与校验依据）。
type FieldType string

const (
	FieldString FieldType = "string" // 短文本
	FieldText   FieldType = "text"   // 长文本（可进向量语料）
	FieldNumber FieldType = "number"
	FieldEnum   FieldType = "enum" // 枚举（值域见 FieldSpec.Enum）
	FieldDate   FieldType = "date" // ISO 8601 日期
)

// VectorRole 字段在语义向量文档中的角色（写入/查询两侧必须对齐）。
type VectorRole string

const (
	VectorNone  VectorRole = "none"
	VectorTitle VectorRole = "title" // 标题（语义匹配的主键位）
	VectorBody  VectorRole = "body"  // 正文（截断拼接）
)

// FieldSpec 单个字段的声明。
type FieldSpec struct {
	Key        string     `json:"key"`
	Label      string     `json:"label"`
	Type       FieldType  `json:"type"`
	Required   bool       `json:"required,omitempty"`
	Enum       []string   `json:"enum,omitempty"`
	Filterable bool       `json:"filterable,omitempty"` // 可作为检索/筛选条件下推
	InVector   VectorRole `json:"in_vector,omitempty"`  // 向量文档组装角色
	InKey      bool       `json:"in_key,omitempty"`     // 参与条目主键 item_key
	// Prompt 提取说明：渲染进分析提示词的「草稿字段规范」段（agent 写入工具的
	// 参数规范与单发降级的输出契约共用）。提示词与 schema 同源——字段增删改，
	// 提示词与写入校验自动跟随。支持 {current_time} 占位符。
	Prompt string `json:"prompt,omitempty"`
}

// DatasetSchema 一个数据集类型的字段合同。
type DatasetSchema struct {
	Type    string      `json:"type"`
	Label   string      `json:"label"`
	Version int         `json:"version"`
	Fields  []FieldSpec `json:"fields"`
}

// Field 按 key 取字段声明。
func (s DatasetSchema) Field(key string) (FieldSpec, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// KeyFields 参与条目主键的字段（按声明序）。
func (s DatasetSchema) KeyFields() []FieldSpec {
	var out []FieldSpec
	for _, f := range s.Fields {
		if f.InKey {
			out = append(out, f)
		}
	}
	return out
}

// Schemas 全部已注册数据集 schema（目录：创建/筛选入口展示用）。
func Schemas() []DatasetSchema {
	return []DatasetSchema{RequirementSchema()}
}

// SchemaOf 按数据集类型取 schema；未注册返回 false。
func SchemaOf(typ string) (DatasetSchema, bool) {
	for _, s := range Schemas() {
		if s.Type == typ {
			return s, true
		}
	}
	return DatasetSchema{}, false
}

// DatasetTypeOfTask 任务类型 → 产出数据集类型（task↔dataset 接缝的类型映射）。
func DatasetTypeOfTask(taskType string) (string, bool) {
	switch taskType {
	case TaskTypeRequirementImport:
		return DatasetTypeRequirement, true
	}
	return "", false
}

// RequirementSchema 需求数据集 schema（与 DraftItem 字段形状对齐）。
// item_key = 归一化标题 + 归一化分组：同组同名视为同一条需求。
// Prompt 为分析提示词的提取说明（提示词与 schema 同源的落点）。
func RequirementSchema() DatasetSchema {
	return DatasetSchema{
		Type:    DatasetTypeRequirement,
		Label:   "需求",
		Version: 1,
		Fields: []FieldSpec{
			{Key: "title", Label: "标题", Type: FieldString, Required: true, InVector: VectorTitle, InKey: true,
				Prompt: "工作项的简洁标题，10-50 字，动宾结构，如「实现用户登录功能」"},
			{Key: "project_name", Label: "分组", Type: FieldString, InKey: true,
				Prompt: "该工作项所属的项目名称。从文档中识别项目名称；未明确时根据需求内容推断合理名称；多项目需求需正确区分"},
			{Key: "description", Label: "描述", Type: FieldText, InVector: VectorBody,
				Prompt: "详细描述，包含需求背景、实现细节、验收标准，保留原文关键信息"},
			{Key: "solution_suggestion", Label: "解决方案建议", Type: FieldText, InVector: VectorBody,
				Prompt: "解决方案建议，具体可执行、含技术细节；按类型给出：story/feature 给开发建议与技术选型，task 给实现步骤，bug 给排查思路与修复建议，epic 给拆分建议与风险点"},
			{Key: "priority", Label: "优先级", Type: FieldEnum, Enum: []string{"High", "Medium", "Low"}, Filterable: true,
				Prompt: "优先级（核心/紧急/阻塞 → High；常规 → Medium；非核心 → Low），默认 Medium"},
			{Key: "type_id", Label: "类型", Type: FieldEnum, Enum: []string{"story", "task", "bug", "feature", "epic"}, Filterable: true,
				Prompt: "工作项类型（story=用户故事 / task=任务 / bug=缺陷 / feature=特性 / epic=史诗），默认 story"},
			{Key: "estimated_hours", Label: "预估工时", Type: FieldNumber,
				Prompt: "预估工时（小时）。简单任务 1-4；中等任务（单模块/API）8-16；复杂任务（核心功能/架构）24-40；大型任务 40+"},
			{Key: "start_at", Label: "开始日期", Type: FieldDate,
				Prompt: "计划开始时间，ISO 8601 格式，默认使用当前时间: \"{current_time}\""},
			{Key: "end_at", Label: "结束日期", Type: FieldDate,
				Prompt: "计划结束时间，ISO 8601 格式；文档未提及则省略"},
			{Key: "assignee_name", Label: "负责人", Type: FieldString, Filterable: true,
				Prompt: "负责人姓名。文档明确指派时提取（注意「由XXX负责」等表述）；未提及返回 null"},
			{Key: "state", Label: "状态", Type: FieldString, Filterable: true,
				Prompt: "状态名称。仅当文档**明确标注**状态时提取（如「待办/进行中/已完成」）；未提及必须返回 null，不要猜测，不要输出状态 ID"},
		},
	}
}
