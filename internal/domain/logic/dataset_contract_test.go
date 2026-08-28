package logic

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDatasetSchema(t *testing.T) {
	inputA := json.RawMessage(`{
		"required":["title"],
		"properties":{
			"title":{"minLength":1,"type":"string"},
			"specs":{"type":"array","items":{"type":"object","properties":{"value":{"type":"number"}},"required":["value"]}}
		},
		"type":"object"
	}`)
	inputB := json.RawMessage(`{"type":"object","properties":{"specs":{"items":{"properties":{"value":{"type":"number"}},"required":["value"],"type":"object"},"type":"array"},"title":{"type":"string","minLength":1}},"required":["title"]}`)

	normalizedA, hashA, err := NormalizeDatasetSchema(inputA)
	if err != nil {
		t.Fatalf("合法 Schema 应通过: %v", err)
	}
	normalizedB, hashB, err := NormalizeDatasetSchema(inputB)
	if err != nil {
		t.Fatalf("等价 Schema 应通过: %v", err)
	}
	if hashA != hashB || string(normalizedA) != string(normalizedB) {
		t.Fatalf("等价 JSON 应规范化为同一结果\nA=%s\nB=%s", normalizedA, normalizedB)
	}
	if strings.Count(string(normalizedA), `"additionalProperties":false`) != 2 {
		t.Fatalf("每个 object 都应补 additionalProperties=false: %s", normalizedA)
	}
}

func TestNormalizeDatasetSchemaRejectsUnsupportedShapes(t *testing.T) {
	cases := map[string]string{
		`{"type":"string"}`:                                                                  "根节点",
		`{"type":"object","properties":{}}`:                                                  "至少需要一个字段",
		`{"type":"object","properties":{"Bad-Key":{"type":"string"}}}`:                       "snake_case",
		`{"type":"object","properties":{"x":{"type":"null"}}}`:                               "type",
		`{"type":"object","properties":{"x":{"type":"array"}}}`:                              "items",
		`{"type":"object","properties":{"x":{"type":"string","pattern":"["}}}`:               "pattern",
		`{"type":"object","properties":{"x":{"type":"string"}},"required":["missing"]}`:      "不存在",
		`{"type":"object","properties":{"x":{"type":"string","oneOf":[]}}}`:                  "暂不支持",
		`{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":true}`: "必须为 false",
	}
	for payload, want := range cases {
		_, _, err := NormalizeDatasetSchema(json.RawMessage(payload))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("payload=%s 期望包含 %q，got %v", payload, want, err)
		}
	}
}

func TestNormalizeUISchema(t *testing.T) {
	got, err := NormalizeUISchema(nil)
	if err != nil || string(got) != `{}` {
		t.Fatalf("空 UI Schema 应归一化为空对象: %s, %v", got, err)
	}
	if _, err := NormalizeUISchema(json.RawMessage(`[]`)); err == nil {
		t.Fatal("UI Schema 根节点数组应拒绝")
	}
}

func TestNextCommitRange(t *testing.T) {
	from, to, err := NextCommitRange(100, 3)
	if err != nil || from != 101 || to != 103 {
		t.Fatalf("NextCommitRange = %d..%d, %v", from, to, err)
	}
	if _, _, err := NextCommitRange(10, 0); err == nil {
		t.Fatal("空 Batch 应拒绝")
	}
}

func TestNormalizeDatasetItemAndIdentity(t *testing.T) {
	schema, schemaHash, err := NormalizeDatasetSchema(json.RawMessage(`{
		"type":"object",
		"properties":{
			"sku":{"type":"string","minLength":1},
			"voltage":{"type":"number","minimum":0},
			"enabled":{"type":"boolean"},
			"released_at":{"type":"string","format":"date"}
		},
		"required":["sku","voltage"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatasetKeyFields(schema, []string{"sku"}); err != nil {
		t.Fatalf("合法主键应通过: %v", err)
	}
	fields, err := NormalizeDatasetItem(schema, json.RawMessage(`{"voltage":3.3,"sku":"X100","enabled":true,"released_at":"2026-08-28"}`))
	if err != nil {
		t.Fatalf("合法字段应通过: %v", err)
	}
	if string(fields) != `{"enabled":true,"released_at":"2026-08-28","sku":"X100","voltage":3.3}` {
		t.Fatalf("字段规范化结果错误: %s", fields)
	}
	key1, fingerprint1, err := DatasetItemIdentity(schemaHash, []string{"sku"}, fields)
	if err != nil || key1 == "" || fingerprint1 == "" {
		t.Fatalf("身份生成失败: key=%q fp=%q err=%v", key1, fingerprint1, err)
	}
	fields2, _ := NormalizeDatasetItem(schema, json.RawMessage(`{"sku":"X100","voltage":3.4}`))
	key2, fingerprint2, _ := DatasetItemIdentity(schemaHash, []string{"sku"}, fields2)
	if key1 != key2 || fingerprint1 == fingerprint2 {
		t.Fatalf("同主键不同内容应 key 相同、fingerprint 不同")
	}
}

func TestNormalizeDatasetItemRejectsInvalidValues(t *testing.T) {
	schema, _, err := NormalizeDatasetSchema(json.RawMessage(`{
		"type":"object",
		"properties":{
			"sku":{"type":"string"},
			"level":{"type":"string","enum":["p0","p1"]},
			"count":{"type":"integer","minimum":1}
		},
		"required":["sku"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		`{"level":"p0"}`:             "必填",
		`{"sku":"x","level":"p2"}`:   "enum",
		`{"sku":"x","count":1.5}`:    "integer",
		`{"sku":"x","count":0}`:      "不能小于",
		`{"sku":"x","unknown":true}`: "未在 Schema",
		`{"sku":null}`:               "不允许为 null",
	}
	for payload, want := range cases {
		_, err := NormalizeDatasetItem(schema, json.RawMessage(payload))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("payload=%s 期望包含 %q，got %v", payload, want, err)
		}
	}
	if err := ValidateDatasetKeyFields(schema, []string{"missing"}); err == nil {
		t.Fatal("不存在的主键字段应拒绝")
	}
}
