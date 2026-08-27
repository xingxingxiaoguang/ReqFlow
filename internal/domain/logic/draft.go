// 草稿字段袋的 schema 驱动归一化：默认值（Default）/枚举（Enum）/清洗（Clean）
// 全部来自 DatasetSchema 声明——归一化代码不含任何具体字段知识（单一事实源）。
package logic

import (
	"math"
	"regexp"
	"strings"
	"time"

	"reqflow/internal/domain/model"
)

// CurrentTimePlaceholder 运行时刻占位符（Default 与 Prompt 共用的既有约定）。
const CurrentTimePlaceholder = "{current_time}"

var (
	stateIdLike      = regexp.MustCompile(`^[a-f0-9]{24}$`)
	statePlaceholder = regexp.MustCompile(`(?i)^(null|undefined|none|n/a|-)$`)
)

// NormalizeValues 将 LLM 原始输出的单个对象（map）按 schema 归一化为字段袋：
// string/text/date trim；enum 越界或空值回落 Default；number 宽松解析（字符串也收）
// 且须为正数，否则回落 Default；{current_time} 占位渲染为运行时刻；Clean 声明的
// 字段做专项清洗。输出只含 schema 声明的字段（越权键丢弃），无值且无默认的字段缺省。
func NormalizeValues(schema model.DatasetSchema, raw map[string]any, now time.Time) map[string]any {
	out := make(map[string]any, len(schema.Fields))
	for _, f := range schema.Fields {
		switch f.Type {
		case model.FieldNumber:
			if n, ok := AsNumber(raw[f.Key]); ok && n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) {
				out[f.Key] = math.Round(n*100) / 100
				continue
			}
			if def, ok := AsNumber(f.Default); ok && def > 0 {
				out[f.Key] = def
			}
		case model.FieldEnum:
			s := strings.TrimSpace(stringify(raw[f.Key]))
			if s != "" && enumContains(f.Enum, s) {
				out[f.Key] = s
				continue
			}
			if def := strings.TrimSpace(stringify(f.Default)); def != "" && enumContains(f.Enum, def) {
				out[f.Key] = def
			}
		default: // string / text / date
			s := strings.TrimSpace(stringify(raw[f.Key]))
			if f.Clean == model.FieldCleanState {
				s = normalizeStateName(s)
			}
			if s != "" {
				out[f.Key] = s
				continue
			}
			if def := strings.TrimSpace(stringify(f.Default)); def != "" {
				if def == CurrentTimePlaceholder {
					def = now.Format(time.RFC3339)
				}
				out[f.Key] = def
			}
		}
	}
	return out
}

// normalizeStateName 状态名清洗：空/占位词/疑似状态 ID 一律视为未标注。
func normalizeStateName(raw string) string {
	if raw == "" {
		return ""
	}
	if statePlaceholder.MatchString(raw) || stateIdLike.MatchString(raw) {
		return ""
	}
	return raw
}
