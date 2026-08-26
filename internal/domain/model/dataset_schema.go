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
	FieldEnum   FieldType = "enum"   // 枚举（值域见 FieldSpec.Enum）
	FieldDate   FieldType = "date"   // ISO 8601 日期
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
func RequirementSchema() DatasetSchema {
	return DatasetSchema{
		Type:    DatasetTypeRequirement,
		Label:   "需求",
		Version: 1,
		Fields: []FieldSpec{
			{Key: "title", Label: "标题", Type: FieldString, Required: true, InVector: VectorTitle, InKey: true},
			{Key: "project_name", Label: "分组", Type: FieldString, InKey: true},
			{Key: "description", Label: "描述", Type: FieldText, InVector: VectorBody},
			{Key: "solution_suggestion", Label: "解决方案建议", Type: FieldText, InVector: VectorBody},
			{Key: "priority", Label: "优先级", Type: FieldEnum, Enum: []string{"High", "Medium", "Low"}, Filterable: true},
			{Key: "type_id", Label: "类型", Type: FieldEnum, Enum: []string{"story", "task", "bug", "feature", "epic"}, Filterable: true},
			{Key: "estimated_hours", Label: "预估工时", Type: FieldNumber},
			{Key: "start_at", Label: "开始日期", Type: FieldDate},
			{Key: "end_at", Label: "结束日期", Type: FieldDate},
			{Key: "assignee_name", Label: "负责人", Type: FieldString, Filterable: true},
			{Key: "state", Label: "状态", Type: FieldString, Filterable: true},
		},
	}
}
