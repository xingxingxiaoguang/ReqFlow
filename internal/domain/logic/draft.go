package logic

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"reqflow/internal/domain/model"
)

var (
	typeAllowList  = []string{"story", "task", "bug", "feature", "epic"}
	prioAllowList  = []string{"High", "Medium", "Low"}
	stateIdLike    = regexp.MustCompile(`^[a-f0-9]{24}$`)
	statePlaceholder = regexp.MustCompile(`(?i)^(null|undefined|none|n/a|-)$`)
)

// NormalizeDraft 将 LLM 原始输出的单个对象（map）规范化为草稿：
// 白名单校验 + 默认值兜底，杜绝异常值流入导入链路。
func NormalizeDraft(raw map[string]any, now time.Time) model.DraftItem {
	get := func(k string) string {
		if v, ok := raw[k]; ok {
			if s, ok2 := v.(string); ok2 {
				return strings.TrimSpace(s)
			}
			// 数字/布尔等偶发混入时降级为字符串
			if v != nil {
				return strings.TrimSpace(fmt.Sprint(v))
			}
		}
		return ""
	}
	getNum := func(k string) float64 {
		if v, ok := raw[k]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case string:
				var f float64
				if _, err := fmt.Sscanf(n, "%g", &f); err == nil {
					return f
				}
			}
		}
		return 0
	}

	d := model.DraftItem{
		ProjectName:        orDefault(get("project_name"), "未分类项目"),
		Title:              orDefault(get("title"), "未命名工作项"),
		Description:        get("description"),
		Priority:           "Medium",
		EstimatedHours:     8,
		StartAt:            orDefault(get("start_at"), now.Format(time.RFC3339)),
		EndAt:              get("end_at"),
		TypeID:             "story",
		AssigneeName:       get("assignee_name"),
		State:              normalizeStateName(get("state")),
		SolutionSuggestion: get("solution_suggestion"),
	}
	if p := get("priority"); contains(prioAllowList, p) {
		d.Priority = p
	}
	if t := get("type_id"); contains(typeAllowList, t) {
		d.TypeID = t
	}
	if h := getNum("estimated_hours"); h > 0 && !math.IsNaN(h) && !math.IsInf(h, 0) {
		d.EstimatedHours = math.Round(h*100) / 100
	}
	return d
}

// NormalizeDrafts 批量规范化（输入元素须为 map[string]any，其余跳过）。
func NormalizeDrafts(raws []any, now time.Time) []model.DraftItem {
	out := make([]model.DraftItem, 0, len(raws))
	for _, r := range raws {
		if m, ok := r.(map[string]any); ok {
			out = append(out, NormalizeDraft(m, now))
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
