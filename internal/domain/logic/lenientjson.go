package logic

import (
	"encoding/json"
	"strings"
)

// StripCodeFences 剥离 markdown 代码块围栏（```json ... ```）。
func StripCodeFences(text string) string {
	if i := strings.Index(text, "```"); i >= 0 {
		rest := text[i+3:]
		if j := strings.Index(rest, "\n"); j >= 0 {
			rest = rest[j+1:]
			if k := strings.LastIndex(rest, "```"); k >= 0 {
				return strings.TrimSpace(rest[:k])
			}
		}
	}
	return text
}

// ParseSlicedJSONArray 截取首个 '[' 到最后一个 ']' 之间的 JSON 数组并解析；
// 无法得到非空数组时返回 null。
func ParseSlicedJSONArray(text string) []any {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start == -1 || end <= start {
		return nil
	}
	var parsed []any
	if err := json.Unmarshal([]byte(text[start:end+1]), &parsed); err != nil || len(parsed) == 0 {
		return nil
	}
	return parsed
}

// RepairTruncatedJSONArray 修复被截断的 JSON 数组：扫描括号配对（跳过字符串字面量
// 内的括号），定位最后一个完整闭合的数组元素，丢弃其后的残缺内容并补上闭合符。
// 例：'[{"a":1},{"a":2' → '[{"a":1}]'
func RepairTruncatedJSONArray(text string) []any {
	start := strings.Index(text, "[")
	if start == -1 {
		return nil
	}
	closers := make([]byte, 0, 16)
	inString, escaped := false, false
	lastCompleteEnd := -1
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '[', '{':
			if ch == '[' {
				closers = append(closers, ']')
			} else {
				closers = append(closers, '}')
			}
		case ']', '}':
			if len(closers) > 0 {
				closers = closers[:len(closers)-1]
			}
			// 元素对象完整闭合、回到数组第一层时，记为可截断点
			if ch == '}' && len(closers) == 1 {
				lastCompleteEnd = i + 1
			}
		}
	}
	if lastCompleteEnd == -1 {
		return nil
	}
	body := strings.TrimRight(text[start:lastCompleteEnd], " \t\r\n")
	body = strings.TrimSuffix(body, ",") + "]"
	var parsed []any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil || len(parsed) == 0 {
		return nil
	}
	return parsed
}

// ExtractJSONArrayLenient 宽松解析 LLM 原始输出为非空 JSON 数组。
// 按代价从低到高：剥围栏 → 截取 [...] → 修复截断数组；全部失败返回 nil，
// 由调用方决定是否回退完整的非流式重调（避免 token 成本翻倍）。
func ExtractJSONArrayLenient(raw string) []any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	stripped := StripCodeFences(strings.TrimSpace(raw))
	if arr := ParseSlicedJSONArray(stripped); arr != nil {
		return arr
	}
	return RepairTruncatedJSONArray(stripped)
}
