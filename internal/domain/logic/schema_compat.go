// schema 兼容规则引擎与形状校验（M3 受控编辑的写守卫核心；纯函数）。
// 规则表见 METADATA §4.4：✅ 兼容 / ⚠️ 条件放行（需显式确认）/ ❌ 不兼容（硬拦截）。
// check 端点 dry-run 与 PUT 保存共用本引擎——判定口径单一事实源。
package logic

import (
	"fmt"
	"regexp"
	"strings"

	"reqflow/internal/domain/model"
)

// CompatLevel 判定级别。
type CompatLevel string

const (
	CompatOK    CompatLevel = "ok"    // ✅ 兼容，直接放行
	CompatWarn  CompatLevel = "warn"  // ⚠️ 条件放行，保存需显式确认（confirm_risky）
	CompatBlock CompatLevel = "block" // ❌ 不兼容，硬拦截
)

// CompatFinding 单条判定（规则表一行 × 具体字段）。
type CompatFinding struct {
	Level   CompatLevel `json:"level"`
	Rule    string      `json:"rule"`            // 规则标识（field_added / field_removed / ...）
	Field   string      `json:"field,omitempty"` // 涉及字段 key（schema 级变更为空）
	Message string      `json:"message"`         // 人话说明（含处置建议）
}

// CompatReport 判定汇总。
type CompatReport struct {
	Findings     []CompatFinding `json:"findings"`
	Blocked      bool            `json:"blocked"`       // 存在 ❌ 项（不可保存）
	NeedsConfirm bool            `json:"needs_confirm"` // 存在 ⚠️ 项（需 confirm_risky）
	NeedsReembed bool            `json:"needs_reembed"` // 存量数据集语料向量需重嵌（InVector 变更类）
}

func (r *CompatReport) add(f CompatFinding) {
	r.Findings = append(r.Findings, f)
	switch f.Level {
	case CompatBlock:
		r.Blocked = true
	case CompatWarn:
		r.NeedsConfirm = true
	}
}

/* ---- 形状校验（保存前置的硬校验；返回首个错误） ---- */

// fieldKeyPattern 字段 key 白名单：小写 snake_case。字段 key 会被拼进过滤 SQL
// （fieldCondSQL 单引号转义是唯一防线），形状校验在这里收口注入面。
var fieldKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// 提示词文本护栏（METADATA §6.1 提示词注入面收口）。
const (
	MaxFieldPromptLen = 2000 // 单字段提取说明
	MaxLabelLen       = 60
	MaxRoleLen        = 8000  // profile 指令头
	MaxExampleLen     = 16000 // 单发示例
)

// ValidateSchemaShape schema 结构合法性（字段 key/类型/枚举/身份/提示词长度）。
func ValidateSchemaShape(s model.DatasetSchema) error {
	if strings.TrimSpace(s.Type) == "" {
		return fmt.Errorf("schema.type 不能为空")
	}
	if strings.TrimSpace(s.Label) == "" {
		return fmt.Errorf("schema.label 不能为空")
	}
	if len(s.Fields) == 0 {
		return fmt.Errorf("字段列表不能为空")
	}
	seenKey := map[string]bool{}
	titles := 0
	for _, f := range s.Fields {
		if !fieldKeyPattern.MatchString(f.Key) {
			return fmt.Errorf("字段 key %q 非法（须为小写字母开头的 snake_case，≤63 字符）", f.Key)
		}
		if seenKey[f.Key] {
			return fmt.Errorf("字段 key 重复: %s", f.Key)
		}
		seenKey[f.Key] = true
		if len([]rune(f.Label)) > MaxLabelLen {
			return fmt.Errorf("字段「%s」名称超长（≤%d 字符）", f.Key, MaxLabelLen)
		}
		switch f.Type {
		case model.FieldString, model.FieldText, model.FieldNumber, model.FieldDate:
		case model.FieldEnum:
			if len(f.Enum) == 0 {
				return fmt.Errorf("枚举字段「%s」的值域不能为空", f.Key)
			}
			seenEnum := map[string]bool{}
			for _, e := range f.Enum {
				if seenEnum[e] {
					return fmt.Errorf("枚举字段「%s」值域重复: %s", f.Key, e)
				}
				seenEnum[e] = true
			}
		default:
			return fmt.Errorf("字段「%s」类型非法: %s", f.Key, f.Type)
		}
		if f.InVector == model.VectorTitle {
			titles++
		}
		if len([]rune(f.Prompt)) > MaxFieldPromptLen {
			return fmt.Errorf("字段「%s」提取说明超长（≤%d 字符）", f.Key, MaxFieldPromptLen)
		}
		if f.Clean != "" && f.Clean != model.FieldCleanState {
			return fmt.Errorf("字段「%s」清洗声明非法: %s（当前仅支持 %s）", f.Key, f.Clean, model.FieldCleanState)
		}
	}
	if len(s.KeyFields()) == 0 {
		return fmt.Errorf("至少需要一个 InKey 字段（条目主键 item_key 的组成）")
	}
	if titles > 1 {
		return fmt.Errorf("InVector=title 的字段至多一个（语义标题位），当前 %d 个", titles)
	}
	return nil
}

// ValidateProfileText profile 文本护栏（指令头/示例长度；空 Role 非法）。
func ValidateProfileText(role, example string) error {
	if strings.TrimSpace(role) == "" {
		return fmt.Errorf("指令头 Role 不能为空")
	}
	if len([]rune(role)) > MaxRoleLen {
		return fmt.Errorf("指令头超长（≤%d 字符）", MaxRoleLen)
	}
	if len([]rune(example)) > MaxExampleLen {
		return fmt.Errorf("单发示例超长（≤%d 字符）", MaxExampleLen)
	}
	return nil
}

// HasTemplateBraces 模板注入告警：`{{` 序列不是本平台的占位符约定
// （占位符为单花括号 {field_spec}/{current_time}），出现双花括号疑似
// 模板注入或粘贴了别家模板——告警不拦截（METADATA §6.1）。
func HasTemplateBraces(text string) bool { return strings.Contains(text, "{{") }

/* ---- 兼容规则引擎 ---- */

// CheckSchemaCompat 新旧 schema 对比，按规则表逐条判定。
// 影响面（哪些存量数据集需重建/重嵌）由 app 层结合数据集清单补充，本函数只判 schema 本身。
func CheckSchemaCompat(old, new model.DatasetSchema) CompatReport {
	var r CompatReport
	oldIdx := indexFields(old)
	newIdx := indexFields(new)

	// 旧 → 新：删除与既有字段变更
	for _, o := range old.Fields {
		n, exists := newIdx[o.Key]
		if !exists {
			r.add(CompatFinding{Level: CompatBlock, Rule: "field_removed", Field: o.Key,
				Message: fmt.Sprintf("删除字段「%s」（%s）：存量条目 fields JSON 与读侧（表格/筛选/向量组装）断裂", o.Key, o.Label)})
			continue
		}
		compareField(&r, o, n)
	}
	// 新 → 旧：新增字段
	for _, n := range new.Fields {
		if _, exists := oldIdx[n.Key]; exists {
			continue
		}
		switch {
		case n.InKey:
			r.add(CompatFinding{Level: CompatBlock, Rule: "in_key_change", Field: n.Key,
				Message: fmt.Sprintf("新增字段「%s」直接进主键：item_key 语义漂移，upsert 基准失效会产生重复条目；请先以非主键字段过渡", n.Key)})
		case n.Required:
			r.add(CompatFinding{Level: CompatWarn, Rule: "field_added_required", Field: n.Key,
				Message: fmt.Sprintf("新增必填字段「%s」：仅对新写入生效，存量条目无此值；建议先以可选过渡再收紧", n.Key)})
		default:
			r.add(CompatFinding{Level: CompatOK, Rule: "field_added", Field: n.Key,
				Message: fmt.Sprintf("新增可选字段「%s」：存量条目按空值处理，兼容", n.Key)})
		}
	}
	// schema 级文案
	if old.Label != new.Label {
		r.add(CompatFinding{Level: CompatOK, Rule: "label_changed",
			Message: fmt.Sprintf("schema 名称「%s」→「%s」：纯展示层，兼容", old.Label, new.Label)})
	}
	// 提示词模板注入告警（新增/变更后的 Prompt 含 {{ 时）
	for _, n := range new.Fields {
		if HasTemplateBraces(n.Prompt) {
			r.add(CompatFinding{Level: CompatWarn, Rule: "prompt_pattern", Field: n.Key,
				Message: fmt.Sprintf("字段「%s」提取说明含 {{ 序列：疑似模板注入或粘贴了外部模板（本平台占位符为单花括号），请确认", n.Key)})
		}
	}
	if len(r.Findings) == 0 {
		r.add(CompatFinding{Level: CompatOK, Rule: "unchanged", Message: "未检测到 schema 变更（或仅默认值/清洗等写入侧属性变化）"})
	}
	return r
}

// compareField 既有字段的前后对比（同 key）。
func compareField(r *CompatReport, o, n model.FieldSpec) {
	// 枚举放宽为自由文本（enum→string）：存量枚举值仍是合法字符串，按放宽处理
	// （compareEnum 的 enum_relaxed 判定）；其余类型变更一律按断裂拦截。
	if o.Type != n.Type && !(o.Type == model.FieldEnum && n.Type == model.FieldString) {
		r.add(CompatFinding{Level: CompatBlock, Rule: "type_changed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」类型 %s → %s：存量值形状与读侧断裂", o.Key, o.Type, n.Type)})
	}
	if o.InKey != n.InKey {
		who := "退出主键"
		if n.InKey {
			who = "加入主键"
		}
		r.add(CompatFinding{Level: CompatBlock, Rule: "in_key_change", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」%s：item_key 语义漂移，upsert 基准失效会产生重复条目", o.Key, who)})
	}
	if o.InVector != n.InVector {
		r.NeedsReembed = true
		r.add(CompatFinding{Level: CompatWarn, Rule: "in_vector_changed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」向量角色 %s → %s：语义向量组装口径变化，存量数据集语料需重嵌（重写任务数据集即可触发）", o.Key, roleLabel(o.InVector), roleLabel(n.InVector))})
	}
	compareEnum(r, o, n)
	if o.Required != n.Required {
		if n.Required {
			r.add(CompatFinding{Level: CompatWarn, Rule: "required_tightened", Field: o.Key,
				Message: fmt.Sprintf("字段「%s」改为必填：存量条目可能缺值，重写时会被校验拦截；仅对新写入生效", o.Key)})
		} else {
			r.add(CompatFinding{Level: CompatOK, Rule: "required_loosened", Field: o.Key,
				Message: fmt.Sprintf("字段「%s」改为可选：约束放宽，兼容", o.Key)})
		}
	}
	if o.Label != n.Label || o.Prompt != n.Prompt {
		r.add(CompatFinding{Level: CompatOK, Rule: "text_changed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」名称/提取说明变更：纯展示与提示词层，兼容", o.Key)})
	}
	if fmt.Sprintf("%v", o.Default) != fmt.Sprintf("%v", n.Default) || o.Clean != n.Clean || o.Filterable != n.Filterable {
		r.add(CompatFinding{Level: CompatOK, Rule: "write_attrs_changed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」默认值/清洗/可筛属性变更：仅影响后续写入与筛选入口，兼容", o.Key)})
	}
}

// compareEnum 枚举值域对比（扩值 ✅ / 收窄 ❌ / 约束增删按方向判定）。
func compareEnum(r *CompatReport, o, n model.FieldSpec) {
	if len(o.Enum) == 0 && len(n.Enum) == 0 {
		return
	}
	if len(o.Enum) > 0 && len(n.Enum) == 0 {
		r.add(CompatFinding{Level: CompatOK, Rule: "enum_relaxed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」移除枚举值域：约束放宽，兼容", o.Key)})
		return
	}
	if len(o.Enum) == 0 && len(n.Enum) > 0 {
		r.add(CompatFinding{Level: CompatBlock, Rule: "enum_narrowed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」新增枚举值域约束：存量自由值可能越界，视同收窄拦截", o.Key)})
		return
	}
	oldSet := map[string]bool{}
	for _, e := range o.Enum {
		oldSet[e] = true
	}
	newSet := map[string]bool{}
	for _, e := range n.Enum {
		newSet[e] = true
	}
	var removed, added []string
	for _, e := range o.Enum {
		if !newSet[e] {
			removed = append(removed, e)
		}
	}
	for _, e := range n.Enum {
		if !oldSet[e] {
			added = append(added, e)
		}
	}
	if len(removed) > 0 {
		r.add(CompatFinding{Level: CompatBlock, Rule: "enum_narrowed", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」枚举收窄（移除 %v）：存量条目可能出现越界值", o.Key, removed)})
	}
	if len(added) > 0 {
		r.add(CompatFinding{Level: CompatOK, Rule: "enum_expanded", Field: o.Key,
			Message: fmt.Sprintf("字段「%s」枚举扩值（新增 %v）：新值可写，旧条目仍合法，兼容", o.Key, added)})
	}
}

func roleLabel(v model.VectorRole) string {
	if v == model.VectorNone || v == "" {
		return "无"
	}
	return string(v)
}

func indexFields(s model.DatasetSchema) map[string]model.FieldSpec {
	idx := make(map[string]model.FieldSpec, len(s.Fields))
	for _, f := range s.Fields {
		idx[f.Key] = f
	}
	return idx
}
