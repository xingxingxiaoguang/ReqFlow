package logic

import "reqflow/internal/domain/model"

/* ---- 优先级：文本档位 → 目标项目优先级 UUID 的别名归一 ---- */

// PriorityAliases 中英文优先级别名词典（小写）。
// 用于把 LLM 输出的 High/Medium/Low 映射到目标项目中名为「最高/高/中/低」等的优先级。
var PriorityAliases = map[string][]string{
	"high":   {"最高", "高", "紧急", "urgent", "critical", "high", "highest"},
	"medium": {"中", "普通", "normal", "medium", "middle"},
	"low":    {"最低", "低", "low", "minor", "lowest"},
}

// ResolvePriorityID 将优先级（ID 或名称）解析为目标项目的优先级 UUID。
// 输入已是 UUID 样式则直接返回；否则按名称与别名词典匹配；未命中返回空。
func ResolvePriorityID(priorityID, priorityName string, priorities []model.MetaPriority) string {
	if isIDLike(priorityID) {
		return priorityID
	}
	lower := map[string]string{}
	for _, p := range priorities {
		if p.Name != "" {
			lower[normalizeKey(p.Name)] = p.ID
		}
	}
	if id := lower[normalizeKey(priorityID)]; id != "" {
		return id
	}
	if id := lower[normalizeKey(priorityName)]; id != "" {
		return id
	}
	// 输入命中某档位的任一别名时，用该档位的全部别名回查项目优先级名
	inKey := normalizeKey(priorityName)
	if inKey == "" {
		inKey = normalizeKey(priorityID)
	}
	for _, aliases := range PriorityAliases {
		for _, alias := range aliases {
			if inKey != alias {
				continue
			}
			for _, candidate := range aliases {
				if id := lower[candidate]; id != "" {
					return id
				}
			}
		}
	}
	return ""
}

/* ---- 工作项类型：名称/组名 → 类型 UUID ---- */

// ResolveTypeID 将类型（ID、名称或组名）解析为目标项目的工作项类型 UUID。
// story/task/bug/feature/epic 这类系统组名与本地化名称（如「缺陷」）均可命中。
func ResolveTypeID(typeID string, types []model.MetaType) string {
	if isIDLike(typeID) {
		return typeID
	}
	byName := map[string]string{}
	for _, t := range types {
		if t.Name != "" {
			byName[normalizeKey(t.Name)] = t.ID
		}
		if t.Group != "" {
			byName[normalizeKey(t.Group)] = t.ID
		}
	}
	if id := byName[normalizeKey(typeID)]; id != "" {
		return id
	}
	if id := byName["story"]; id != "" { // 默认回退用户故事类型
		return id
	}
	if typeID != "" {
		return typeID
	}
	return "story"
}

/* ---- 工时：小时 ↔ 平台工时单位 ---- */

// UnitHours 平台工时单位换算为小时的系数。
var UnitHours = map[string]float64{"minute": 1.0 / 60, "hour": 1, "day": 8}

// HoursToWorkload 小时 → 平台工时单位数值（保留 3 位小数）；非法输入返回 0。
func HoursToWorkload(hours float64, unit string) float64 {
	factor, ok := UnitHours[unit]
	if !ok || hours <= 0 {
		return 0
	}
	v := hours / factor
	return mathRound3(v)
}

func mathRound3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}

/* ---- 内部工具 ---- */

var idLikeLen = []int{36, 32, 24}

// isIDLike 判断字符串是否形似 UUID / 32 位 hex / 24 位 ObjectId（已是 ID 无需再映射）。
func isIDLike(s string) bool {
	if s == "" {
		return false
	}
	for _, n := range idLikeLen {
		if len(s) != n {
			continue
		}
		isHex := true
		for _, c := range s {
			switch {
			case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F', c == '-':
			default:
				isHex = false
			}
			if !isHex {
				break
			}
		}
		if isHex {
			return true
		}
	}
	return false
}

func normalizeKey(s string) string {
	return NormalizeForExactMatch(s)
}
