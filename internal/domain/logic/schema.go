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

// FingerprintOf 条目指纹：schema 全部字段值 canonical 序列化的 SHA-256。
// 指纹相同 = 内容未变（跳过更新与重新向量化）。
func FingerprintOf(schema model.DatasetSchema, values map[string]any) string {
	var b strings.Builder
	for _, f := range schema.Fields {
		b.WriteString(f.Key)
		b.WriteByte('=')
		b.WriteString(stringify(values[f.Key]))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
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
