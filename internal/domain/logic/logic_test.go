package logic

import (
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

func TestNormalizeValues(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	raw := map[string]any{
		"project_name":    "  用户中心 ",
		"title":           "",
		"priority":        "critical", // 枚举越界 → 回落 Default(Medium)
		"estimated_hours": "16",       // 字符串数字宽松解析
		"type_id":         "story",
		"state":           "null", // 占位词 → 清洗为空（无默认 → 缺省）
		"assignee_name":   "张三",
		"extra_key":       "越权键应丢弃",
	}
	v := NormalizeValues(model.RequirementSchema(), raw, now)
	if v["project_name"] != "用户中心" || v["title"] != "未命名工作项" {
		t.Errorf("默认值兜底失败: %+v", v)
	}
	if v["priority"] != "Medium" {
		t.Errorf("枚举越界应回落默认: %v", v["priority"])
	}
	if v["estimated_hours"] != float64(16) {
		t.Errorf("字符串数字解析失败: %v", v["estimated_hours"])
	}
	if v["start_at"] != now.Format(time.RFC3339) {
		t.Errorf("{current_time} 占位应渲染运行时刻: %v", v["start_at"])
	}
	if _, ok := v["state"]; ok {
		t.Errorf("清洗为空且无默认的字段应缺省: %+v", v)
	}
	if _, ok := v["extra_key"]; ok {
		t.Errorf("schema 外的键应丢弃: %+v", v)
	}
}

func TestNormalizeValuesNegativeNumberFallsBack(t *testing.T) {
	v := NormalizeValues(model.RequirementSchema(), map[string]any{"estimated_hours": -1}, time.Now())
	if v["estimated_hours"] != float64(8) {
		t.Errorf("非正数应回落默认: %v", v["estimated_hours"])
	}
}

func TestNormalizeValuesCustomSchema(t *testing.T) {
	// 自定义 schema（零 requirement 知识）：默认值/枚举/清洗全部随 schema 声明生效
	schema := model.DatasetSchema{Type: "review_note", Fields: []model.FieldSpec{
		{Key: "finding", Label: "发现", Type: model.FieldString, Required: true, InKey: true,
			InVector: model.VectorTitle, Default: "未命名发现"},
		{Key: "severity", Label: "级别", Type: model.FieldEnum, Enum: []string{"p0", "p1", "p2"}, Default: "p1"},
		{Key: "hours", Label: "耗时", Type: model.FieldNumber},
		{Key: "state", Label: "状态", Type: model.FieldString, Clean: model.FieldCleanState},
	}}
	now := time.Now()
	v := NormalizeValues(schema, map[string]any{"finding": "  空指针  ", "severity": "p9", "hours": "1.5", "state": "666f6f2062617262617a7175"}, now)
	if v["finding"] != "空指针" {
		t.Errorf("trim 失败: %v", v["finding"])
	}
	if v["severity"] != "p1" {
		t.Errorf("自定义枚举越界应回落自定义默认: %v", v["severity"])
	}
	if v["hours"] != 1.5 {
		t.Errorf("数字解析失败: %v", v["hours"])
	}
	if _, ok := v["state"]; ok {
		t.Errorf("疑似 ID 应清洗为空且缺省: %+v", v)
	}
	v2 := NormalizeValues(schema, nil, now)
	if v2["finding"] != "未命名发现" || v2["severity"] != "p1" {
		t.Errorf("全空输入应填自定义默认: %+v", v2)
	}
	if _, ok := v2["hours"]; ok {
		t.Errorf("无默认的空数字应缺省: %+v", v2)
	}
}

func TestTitleFieldOf(t *testing.T) {
	f, ok := TitleFieldOf(model.RequirementSchema())
	if !ok || f.Key != "title" {
		t.Fatalf("requirement 标题字段 = %+v ok=%v", f, ok)
	}
	if _, ok := TitleFieldOf(model.DatasetSchema{Type: "x"}); ok {
		t.Fatal("无 title 角色的 schema 应返回 false")
	}
}

func TestItemKeyOf(t *testing.T) {
	schema := model.RequirementSchema()
	a := ItemKeyOf(schema, map[string]any{"title": "实现登录", "project_name": "UserCenter"})
	b := ItemKeyOf(schema, map[string]any{"title": "实现登录", "project_name": "ＵｓｅｒＣｅｎｔｅｒ"})
	if a != b {
		t.Errorf("归一化后相同的标题/分组应为同一 key: %q vs %q", a, b)
	}
	c := ItemKeyOf(schema, map[string]any{"title": "实现登录", "project_name": "订单中心"})
	if a == c {
		t.Error("不同分组应为不同 key")
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
