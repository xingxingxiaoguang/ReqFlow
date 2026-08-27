package logic

// 兼容规则引擎全规则测试（METADATA §4.4 规则表逐行 × 兼容/不兼容用例）
// + 形状校验护栏。这是 M3 写守卫的判定口径，规则表改动必须同步本测试。

import (
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func baseSchema() model.DatasetSchema {
	return model.DatasetSchema{
		Type: "t", Label: "测试", Version: 1,
		Fields: []model.FieldSpec{
			{Key: "title", Label: "标题", Type: model.FieldString, Required: true, InKey: true, InVector: model.VectorTitle},
			{Key: "desc", Label: "描述", Type: model.FieldText, InVector: model.VectorBody},
			{Key: "priority", Label: "优先级", Type: model.FieldEnum, Enum: []string{"High", "Medium", "Low"}, Filterable: true},
		},
	}
}

func findingRules(r CompatReport, level CompatLevel, rule string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Level == level && f.Rule == rule {
			n++
		}
	}
	return n
}

func TestCompatUnchanged(t *testing.T) {
	r := CheckSchemaCompat(baseSchema(), baseSchema())
	if r.Blocked || r.NeedsConfirm || r.NeedsReembed {
		t.Fatalf("无变更不应有告警/拦截: %+v", r)
	}
	if findingRules(r, CompatOK, "unchanged") != 1 {
		t.Fatalf("应有一条 unchanged 判定: %+v", r.Findings)
	}
}

func TestCompatAddOptionalField(t *testing.T) {
	next := baseSchema()
	next.Fields = append(next.Fields, model.FieldSpec{Key: "owner", Label: "负责人", Type: model.FieldString})
	r := CheckSchemaCompat(baseSchema(), next)
	if r.Blocked || r.NeedsConfirm {
		t.Fatalf("新增可选字段应兼容: %+v", r)
	}
	if findingRules(r, CompatOK, "field_added") != 1 {
		t.Fatalf("缺 field_added 判定: %+v", r.Findings)
	}
}

func TestCompatAddRequiredField(t *testing.T) {
	next := baseSchema()
	next.Fields = append(next.Fields, model.FieldSpec{Key: "owner", Label: "负责人", Type: model.FieldString, Required: true})
	r := CheckSchemaCompat(baseSchema(), next)
	if r.Blocked || !r.NeedsConfirm {
		t.Fatalf("新增必填字段应 ⚠️ 需确认: %+v", r)
	}
	if findingRules(r, CompatWarn, "field_added_required") != 1 {
		t.Fatalf("缺 field_added_required 判定: %+v", r.Findings)
	}
}

func TestCompatRemoveField(t *testing.T) {
	next := baseSchema()
	next.Fields = next.Fields[:2]
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.Blocked {
		t.Fatal("删除字段应 ❌ 拦截")
	}
	if findingRules(r, CompatBlock, "field_removed") != 1 {
		t.Fatalf("缺 field_removed 判定: %+v", r.Findings)
	}
}

func TestCompatTypeChanged(t *testing.T) {
	next := baseSchema()
	next.Fields[1].Type = model.FieldString
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.Blocked || findingRules(r, CompatBlock, "type_changed") != 1 {
		t.Fatalf("改字段类型应 ❌ 拦截: %+v", r)
	}
}

func TestCompatInKeyChange(t *testing.T) {
	// 既有字段加入主键
	next := baseSchema()
	next.Fields[2].InKey = true
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.Blocked || findingRules(r, CompatBlock, "in_key_change") != 1 {
		t.Fatalf("InKey 变更应 ❌ 拦截: %+v", r)
	}
	// 新增字段直接进主键
	next2 := baseSchema()
	next2.Fields = append(next2.Fields, model.FieldSpec{Key: "group", Label: "分组", Type: model.FieldString, InKey: true})
	r2 := CheckSchemaCompat(baseSchema(), next2)
	if !r2.Blocked || findingRules(r2, CompatBlock, "in_key_change") != 1 {
		t.Fatalf("新增字段进主键应 ❌ 拦截: %+v", r2)
	}
}

func TestCompatEnumExpand(t *testing.T) {
	next := baseSchema()
	next.Fields[2].Enum = []string{"High", "Medium", "Low", "Urgent"}
	r := CheckSchemaCompat(baseSchema(), next)
	if r.Blocked || r.NeedsConfirm {
		t.Fatalf("枚举扩值应兼容: %+v", r)
	}
	if findingRules(r, CompatOK, "enum_expanded") != 1 {
		t.Fatalf("缺 enum_expanded 判定: %+v", r.Findings)
	}
}

func TestCompatEnumNarrow(t *testing.T) {
	next := baseSchema()
	next.Fields[2].Enum = []string{"High", "Medium"}
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.Blocked || findingRules(r, CompatBlock, "enum_narrowed") != 1 {
		t.Fatalf("枚举收窄应 ❌ 拦截: %+v", r)
	}
}

func TestCompatEnumConstraintAdded(t *testing.T) {
	// 自由文本字段新增值域约束 = 事实上的收窄
	next := baseSchema()
	next.Fields[1].Type = model.FieldEnum
	next.Fields[1].Enum = []string{"a", "b"}
	r := CheckSchemaCompat(baseSchema(), next)
	if findingRules(r, CompatBlock, "enum_narrowed") != 1 || findingRules(r, CompatBlock, "type_changed") != 1 {
		t.Fatalf("自由字段加值域应拦截（含类型变更）: %+v", r)
	}
}

func TestCompatEnumConstraintRemoved(t *testing.T) {
	// 枚举字段放宽为自由文本：值域移除 + 类型改 string
	next := baseSchema()
	next.Fields[2].Type = model.FieldString
	next.Fields[2].Enum = nil
	r := CheckSchemaCompat(baseSchema(), next)
	if findingRules(r, CompatOK, "enum_relaxed") != 1 {
		t.Fatalf("移除值域约束应兼容: %+v", r)
	}
	if r.Blocked {
		t.Fatalf("放宽不应拦截: %+v", r)
	}
}

func TestCompatTextChange(t *testing.T) {
	next := baseSchema()
	next.Fields[0].Label = "新标题"
	next.Fields[0].Prompt = "新的提取说明"
	next.Label = "新名称"
	r := CheckSchemaCompat(baseSchema(), next)
	if r.Blocked || r.NeedsConfirm {
		t.Fatalf("文案变更应兼容: %+v", r)
	}
	if findingRules(r, CompatOK, "text_changed") != 1 || findingRules(r, CompatOK, "label_changed") != 1 {
		t.Fatalf("缺文案判定: %+v", r.Findings)
	}
}

func TestCompatInVectorChange(t *testing.T) {
	next := baseSchema()
	next.Fields[1].InVector = model.VectorNone
	r := CheckSchemaCompat(baseSchema(), next)
	if r.Blocked || !r.NeedsConfirm || !r.NeedsReembed {
		t.Fatalf("InVector 变更应 ⚠️ 需重嵌: %+v", r)
	}
	if findingRules(r, CompatWarn, "in_vector_changed") != 1 {
		t.Fatalf("缺 in_vector_changed 判定: %+v", r.Findings)
	}
}

func TestCompatRequiredTightened(t *testing.T) {
	next := baseSchema()
	next.Fields[1].Required = true
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.NeedsConfirm || findingRules(r, CompatWarn, "required_tightened") != 1 {
		t.Fatalf("收紧必填应 ⚠️: %+v", r)
	}
}

func TestCompatPromptPatternWarning(t *testing.T) {
	next := baseSchema()
	next.Fields[0].Prompt = "提取 {{user.name}} 字段"
	r := CheckSchemaCompat(baseSchema(), next)
	if !r.NeedsConfirm || findingRules(r, CompatWarn, "prompt_pattern") != 1 {
		t.Fatalf("{{ 模式应告警: %+v", r)
	}
}

func TestValidateSchemaShape(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*model.DatasetSchema)
		msg   string
	}{
		{"空类型", func(s *model.DatasetSchema) { s.Type = "" }, "type"},
		{"空字段", func(s *model.DatasetSchema) { s.Fields = nil }, "字段"},
		{"key 非法", func(s *model.DatasetSchema) { s.Fields[0].Key = "Bad-Key" }, "key"},
		{"key 重复", func(s *model.DatasetSchema) {
			s.Fields = append(s.Fields, model.FieldSpec{Key: "title", Label: "x", Type: model.FieldString})
		}, "重复"},
		{"类型非法", func(s *model.DatasetSchema) { s.Fields[1].Type = "blob" }, "类型"},
		{"枚举空值域", func(s *model.DatasetSchema) {
			s.Fields[2].Type = model.FieldEnum
			s.Fields[2].Enum = nil
		}, "值域"},
		{"无主键字段", func(s *model.DatasetSchema) { s.Fields[0].InKey = false }, "InKey"},
		{"双标题位", func(s *model.DatasetSchema) { s.Fields[1].InVector = model.VectorTitle }, "title"},
		{"清洗声明非法", func(s *model.DatasetSchema) { s.Fields[1].Clean = "hack" }, "清洗"},
		{"提取说明超长", func(s *model.DatasetSchema) {
			s.Fields[1].Prompt = strings.Repeat("长", MaxFieldPromptLen+1)
		}, "超长"},
	}
	for _, c := range cases {
		s := baseSchema()
		c.mut(&s)
		err := ValidateSchemaShape(s)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%s: 期望报错含 %q，得到 %v", c.name, c.msg, err)
		}
	}
	if err := ValidateSchemaShape(baseSchema()); err != nil {
		t.Fatalf("合法 schema 不应报错: %v", err)
	}
}

func TestValidateProfileText(t *testing.T) {
	if err := ValidateProfileText("", "x"); err == nil {
		t.Fatal("空 Role 应报错")
	}
	if err := ValidateProfileText(strings.Repeat("a", MaxRoleLen+1), ""); err == nil {
		t.Fatal("超长 Role 应报错")
	}
	if err := ValidateProfileText("角色", strings.Repeat("a", MaxExampleLen+1)); err == nil {
		t.Fatal("超长 Example 应报错")
	}
	if err := ValidateProfileText("角色", "示例"); err != nil {
		t.Fatalf("合法 profile 不应报错: %v", err)
	}
}
