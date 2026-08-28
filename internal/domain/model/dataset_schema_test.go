package model

import "testing"

func TestParseDatasetSchema(t *testing.T) {
	// 合法载荷：类型 + 非空字段定义
	got, ok := ParseDatasetSchema(`{"type":"review_note","label":"评审发现","version":2,"fields":[{"key":"finding","label":"发现","type":"string","fts":true}]}`)
	if !ok || got.Type != "review_note" || got.Version != 2 || len(got.Fields) != 1 || !got.Fields[0].FTS {
		t.Fatalf("ParseDatasetSchema = %+v ok=%v", got, ok)
	}
	// 空载荷 / 非法 JSON / 缺字段定义：一律 false（调用方按定义缺失处理，不做类型兜底）
	for _, payload := range []string{"", "   ", `{bad`, `{"type":"x"}`, `{"type":"x","fields":[]}`} {
		if s, ok := ParseDatasetSchema(payload); ok {
			t.Fatalf("载荷 %q 应解析失败，got %+v", payload, s)
		}
	}
}

func TestRequirementSchemaHasFTSFields(t *testing.T) {
	// 模板应至少含一个全文检索字段（新建数据集带出后 FTS 索引有据可建）
	sc := RequirementSchema()
	n := 0
	for _, f := range sc.Fields {
		if f.FTS {
			n++
		}
	}
	if n == 0 {
		t.Fatal("requirement 模板应含 FTS 字段")
	}
}
