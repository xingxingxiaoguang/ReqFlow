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

func TestResolveTypeIDAndPriority(t *testing.T) {
	types := []model.MetaType{
		{ID: "uuid-story", ProjectID: "p1", Name: "用户故事", Group: "story"},
		{ID: "uuid-bug", ProjectID: "p1", Name: "缺陷", Group: "bug"},
	}
	if got := ResolveTypeID("bug", types); got != "uuid-bug" {
		t.Errorf("按组名解析失败: %s", got)
	}
	if got := ResolveTypeID("缺陷", types); got != "uuid-bug" {
		t.Errorf("按名称解析失败: %s", got)
	}
	if got := ResolveTypeID("不存在的", types); got != "uuid-story" {
		t.Errorf("未知类型应回退 story: %s", got)
	}
	if got := ResolveTypeID("550e8400-e29b-41d4-a716-446655440000", types); got != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("UUID 应直通: %s", got)
	}

	prios := []model.MetaPriority{
		{ID: "uuid-high", ProjectID: "p1", Name: "最高"},
		{ID: "uuid-mid", ProjectID: "p1", Name: "中"},
		{ID: "uuid-low", ProjectID: "p1", Name: "低"},
	}
	if got := ResolvePriorityID("", "High", prios); got != "uuid-high" {
		t.Errorf("别名 High→最高 失败: %s", got)
	}
	if got := ResolvePriorityID("", "中", prios); got != "uuid-mid" {
		t.Errorf("中文名解析失败: %s", got)
	}
	if got := ResolvePriorityID("", "unknown", prios); got != "" {
		t.Errorf("未命中应返回空: %s", got)
	}
}

func TestHoursToWorkload(t *testing.T) {
	if v := HoursToWorkload(8, "hour"); v != 8 {
		t.Errorf("hour 单位换算错误: %v", v)
	}
	if v := HoursToWorkload(8, "day"); v != 1 {
		t.Errorf("day 单位换算错误: %v", v)
	}
	if v := HoursToWorkload(1, "minute"); v != 60 {
		t.Errorf("minute 单位换算错误: %v", v)
	}
	if v := HoursToWorkload(-1, "hour"); v != 0 {
		t.Errorf("非法工时应返回 0: %v", v)
	}
}

func TestItemChanged(t *testing.T) {
	if ItemChanged("t", "d", "r1", false, "t", "d", "r1") {
		t.Error("无变化不应更新")
	}
	if !ItemChanged("t", "d", "r1", false, "t", "d", "r2") {
		t.Error("远端更新时间变化应更新")
	}
	if !ItemChanged("t", "d", "r1", true, "t", "d", "r1") {
		t.Error("归档项重现应更新")
	}
	if !ItemChanged("t", "d", "r1", false, "t2", "d", "r1") {
		t.Error("标题变化应更新")
	}
}

// 编译期保证 model 不依赖 logic（防止反向依赖潜入）。
var _ = model.ItemStatusPending

// JSON 序列化冒烟：DraftItem 输出给前端的字段名与 API 契约一致。
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
