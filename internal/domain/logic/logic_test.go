package logic

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reqflow/internal/domain/model"
)

func TestNormalizeForExactMatch(t *testing.T) {
	cases := []struct{ in, want string }{
		{"UserCenter", "usercenter"},
		{"ＵｓｅｒＣｅｎｔｅｒ", "usercenter"},                       // 全角
		{"  需求   管理  系统 ", "需求 管理 系统"},                // 空白压缩
		{"支付 V2 重构", "支付 v2 重构"},
		{"支付　Ｖ２　重构", "支付 v2 重构"},                       // 全角空格+全角数字
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeForExactMatch(c.in); got != c.want {
			t.Errorf("NormalizeForExactMatch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractJSONArrayLenient(t *testing.T) {
	// 标准 JSON
	if arr := ExtractJSONArrayLenient(`[{"a":1},{"a":2}]`); len(arr) != 2 {
		t.Errorf("标准数组解析失败: %v", arr)
	}
	// 围栏包裹
	if arr := ExtractJSONArrayLenient("```json\n[{\"a\":1}]\n```"); len(arr) != 1 {
		t.Errorf("围栏剥离失败: %v", arr)
	}
	// 前后夹带说明文字
	if arr := ExtractJSONArrayLenient("好的，以下是结果：\n[{\"a\":1},{\"a\":2}]\n希望有帮助"); len(arr) != 2 {
		t.Errorf("夹带文字截取失败: %v", arr)
	}
	// 截断的数组（流中断）
	if arr := ExtractJSONArrayLenient(`[{"a":1},{"a":2`); len(arr) != 1 {
		t.Errorf("截断修复失败: %v", arr)
	}
	// 字符串字面量内的括号不得干扰配对
	if arr := ExtractJSONArrayLenient(`[{"s":"a]b{c"},{"s":"}"}]`); len(arr) != 2 {
		t.Errorf("字符串内括号干扰: %v", arr)
	}
	// 无 JSON
	if arr := ExtractJSONArrayLenient("抱歉我无法完成"); arr != nil {
		t.Errorf("无 JSON 应返回 nil: %v", arr)
	}
	if arr := ExtractJSONArrayLenient(""); arr != nil {
		t.Errorf("空串应返回 nil: %v", arr)
	}
}

func TestNormalizeDraft(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	raw := map[string]any{
		"project_name": "  用户中心 ",
		"title":        "",
		"priority":     "critical", // 非法值 → Medium
		"estimated_hours": "16",  // 字符串数字
		"type_id":      "story",
		"state":        "null",  // 占位 → 空
		"assignee_name": "张三",
	}
	d := NormalizeDraft(raw, now)
	if d.ProjectName != "用户中心" || d.Title != "未命名工作项" {
		t.Errorf("默认值兜底失败: %+v", d)
	}
	if d.Priority != "Medium" {
		t.Errorf("优先级白名单失效: %s", d.Priority)
	}
	if d.EstimatedHours != 16 {
		t.Errorf("字符串数字解析失败: %v", d.EstimatedHours)
	}
	if d.State != "" {
		t.Errorf("占位状态应清空: %q", d.State)
	}
	if d.StartAt == "" {
		t.Error("开始时间缺省应填当前时间")
	}
}

func TestNormalizeDraftsSkipsNonMap(t *testing.T) {
	out := NormalizeDrafts([]any{map[string]any{"title": "a"}, "garbage", 42}, time.Now())
	if len(out) != 1 || out[0].Title != "a" {
		t.Errorf("非 map 元素应跳过: %+v", out)
	}
}

func TestDistanceToScore(t *testing.T) {
	cases := []struct{ d, want float64 }{{0, 1}, {1, 0.5}, {2, 0}, {3, 0}, {-1, 1}}
	for _, c := range cases {
		if got := DistanceToScore(c.d); got != c.want {
			t.Errorf("DistanceToScore(%v) = %v, want %v", c.d, got, c.want)
		}
	}
}

func TestDraftItemJSONShape(t *testing.T) {
	b, err := json.Marshal(model.DraftItem{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"project_name"`, `"solution_suggestion"`, `"estimated_hours"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON 缺少字段 %s: %s", key, b)
		}
	}
}
