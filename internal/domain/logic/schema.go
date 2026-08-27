// 数据集条目身份与 schema 驱动的纯函数（写入侧与预览侧共用）。
package logic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"reqflow/internal/domain/model"
)

// itemKeySep 条目主键内部分隔符：归一化文本不可能出现的 ASCII 单元分隔符。
// 勿用 NUL（\x00）——PG 的 TEXT 列禁止存储 NUL 字节（SQLSTATE 22021）。
const itemKeySep = "\x1f"

// ItemKeyOf 条目主键：schema InKey 字段值归一化后拼接。
// 同一 schema 下 key 相同 = 同一条目（upsert/去重的判定基准）。
func ItemKeyOf(schema model.DatasetSchema, values map[string]any) string {
	parts := make([]string, 0, 4)
	for _, f := range schema.KeyFields() {
		parts = append(parts, NormalizeForExactMatch(stringify(values[f.Key])))
	}
	return strings.Join(parts, itemKeySep)
}

// VectorBodyLimit 向量文档中 body 字段的总截断长度（写入侧/查询侧共用；进指纹盐）。
// 曾在 app 层声明，M3 起下沉 domain——它是向量组装参数的一部分，参与指纹判定。
const VectorBodyLimit = 500

// FingerprintOf 条目指纹：schema 全部字段值 canonical 序列化 + 向量相关 schema 摘要盐
// 的 SHA-256。指纹相同 = 内容未变且向量组装口径未变（跳过更新与重新向量化）。
//
// 盐的必要性（METADATA §4.4 已知缺陷修复）：只哈希字段值时，InVector 角色 / 截断参数
// 变更后 unchanged 条目会跳过重嵌，语料向量与新 schema 不对齐。盐 = InKey 字段集 +
// InVector 角色 + VectorBodyLimit 的摘要——改 Label 等纯展示项不触发全量重嵌。
func FingerprintOf(schema model.DatasetSchema, values map[string]any) string {
	var b strings.Builder
	b.WriteString("#" + VectorFingerprintSalt(schema) + "\n")
	for _, f := range schema.Fields {
		b.WriteString(f.Key)
		b.WriteByte('=')
		b.WriteString(stringify(values[f.Key]))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// VectorFingerprintSalt 向量相关 schema 摘要（盐）：字段 key + InKey/InVector 角色 +
// 截断参数。InKey 影响 item_key 语义（连带条目身份），一并纳入以防直接改库绕过守卫。
func VectorFingerprintSalt(schema model.DatasetSchema) string {
	var b strings.Builder
	b.WriteString(schema.Type)
	b.WriteString("|body_limit=")
	fmt.Fprintf(&b, "%d", VectorBodyLimit)
	for _, f := range schema.Fields {
		b.WriteString("|")
		b.WriteString(f.Key)
		if f.InKey {
			b.WriteString(":key")
		}
		if f.InVector != model.VectorNone {
			b.WriteString(":vec=" + string(f.InVector))
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

// ValidateValues 按 schema 校验字段值（必填 + 枚举值域）；返回首个错误。
// 数字/日期只做非空粗校验——LLM 产出的宽松形状由宽松 JSON 恢复兜底。
func ValidateValues(schema model.DatasetSchema, values map[string]any) error {
	for _, f := range schema.Fields {
		v := stringify(values[f.Key])
		if f.Required && strings.TrimSpace(v) == "" {
			return fmt.Errorf("字段「%s」不能为空", f.Label)
		}
		if f.Type == model.FieldEnum && v != "" && !enumContains(f.Enum, v) {
			return fmt.Errorf("字段「%s」的值 %q 不在允许范围 %v 内", f.Label, v, f.Enum)
		}
	}
	return nil
}

// VectorDocOf 按 schema 组装语义向量文档：Title 行 + Body 字段拼接（各自截断）。
// 写入侧与查询侧共用同一组装规则，保证向量空间对齐。
func VectorDocOf(schema model.DatasetSchema, values map[string]any, bodyLimit int) string {
	var title, body string
	for _, f := range schema.Fields {
		v := strings.TrimSpace(stringify(values[f.Key]))
		switch f.InVector {
		case model.VectorTitle:
			title = v
		case model.VectorBody:
			if v != "" {
				body += v + "\n"
			}
		}
	}
	if bodyLimit > 0 {
		body = truncateRunesLen(body, bodyLimit)
	}
	if body == "" {
		return title
	}
	return title + "\n" + body
}

// stringify 任意字段值 → 字符串（数字保持 Go 默认格式；nil/缺省 = ""）。
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case bool:
		return fmt.Sprintf("%t", x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// AsNumber 数字宽松解析（JSON number/int 或可解析字符串；归一化与写入工具校验共用）。
func AsNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

// TitleFieldOf 语义向量文档的标题字段（InVector==VectorTitle；查重与展示标题的 schema 口径）。
func TitleFieldOf(schema model.DatasetSchema) (model.FieldSpec, bool) {
	for _, f := range schema.Fields {
		if f.InVector == model.VectorTitle {
			return f, true
		}
	}
	return model.FieldSpec{}, false
}

func enumContains(enum []string, v string) bool {
	for _, e := range enum {
		if e == v {
			return true
		}
	}
	return false
}

func truncateRunesLen(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
