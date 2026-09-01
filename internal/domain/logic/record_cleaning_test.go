package logic

import (
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
)

func cleaningTestSchema(t *testing.T) (json.RawMessage, string) {
	t.Helper()
	raw := json.RawMessage(`{
		"type":"object","additionalProperties":false,
		"properties":{
			"sku":{"type":"string","pattern":"^[A-Z]-[0-9]+$"},
			"enabled":{"type":"boolean"},
			"weight_kg":{"type":"number","minimum":0},
			"status":{"type":"string","enum":["active","retired"]},
			"released_on":{"type":"string","format":"date"},
			"tags":{"type":"array","items":{"type":"string"}},
			"display_name":{"type":"string"}
		},
		"required":["sku","enabled","weight_kg","status","released_on","display_name"]
	}`)
	normalized, hash, err := NormalizeDatasetSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	return normalized, hash
}

func TestTransformRecordAppliesControlledDeterministicRules(t *testing.T) {
	schema, _ := cleaningTestSchema(t)
	rules := mustNormalizationRules(t, `[
		{"field":"enabled","operation":"boolean_alias","true_values":["是"],"false_values":["否"]},
		{"field":"weight_kg","operation":"unit_scale","units":{"kg":1,"g":0.001}},
		{"field":"status","operation":"enum_alias","aliases":{"在售":"active","退市":"retired"}},
		{"field":"released_on","operation":"date","layouts":["2006/01/02"]},
		{"field":"tags","operation":"split","separator":","},
		{"field":"display_name","operation":"concat","source_fields":["sku","status"],"separator":" / "}
	]`)
	fields, changes, issues, err := TransformRecord(schema, rules, json.RawMessage(`{
		"sku":" Ａ－１００ ","enabled":"是","weight_kg":"1.5 kg","status":"在售",
		"released_on":"2026/08/29","tags":"电源, 安全"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	var got map[string]any
	if err := decodeSingleJSON(fields, &got); err != nil {
		t.Fatal(err)
	}
	if got["sku"] != "A-100" || got["enabled"] != true || got["status"] != "active" ||
		got["released_on"] != "2026-08-29" || got["display_name"] != "A-100 / active" {
		t.Fatalf("transformed fields=%s", fields)
	}
	if number, ok := got["weight_kg"].(json.Number); !ok || string(number) != "1.5" {
		t.Fatalf("weight=%T(%v)", got["weight_kg"], got["weight_kg"])
	}
	if tags, ok := got["tags"].([]any); !ok || len(tags) != 2 || tags[1] != "安全" {
		t.Fatalf("tags=%v", got["tags"])
	}
	if len(changes) != 7 {
		t.Fatalf("changes=%d %+v", len(changes), changes)
	}
}

func TestValidateTransformedRecordCombinesSchemaAndBusinessRules(t *testing.T) {
	schema, _ := cleaningTestSchema(t)
	rules := mustValidationRules(t, `[
		{"field":"sku","operation":"regex","pattern":"^A-","severity":"warning","message":"建议使用 A 系列编码"},
		{"field":"weight_kg","operation":"range","minimum":0.1,"maximum":100},
		{"field":"display_name","operation":"required"}
	]`)
	fields := json.RawMessage(`{"sku":"B-2","enabled":true,"weight_kg":0.05,"status":"active","released_on":"2026-08-29","display_name":"B-2"}`)
	_, issues, err := ValidateTransformedRecord(schema, rules, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Severity != model.RecordIssueWarning || issues[1].Code != "rule_range" {
		t.Fatalf("issues=%+v", issues)
	}

	_, invalid, err := ValidateTransformedRecord(schema, nil,
		json.RawMessage(`{"sku":"bad","enabled":"not-bool"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(invalid) != 1 || invalid[0].Code != "schema_invalid" || invalid[0].Severity != model.RecordIssueError {
		t.Fatalf("schema issues=%+v", invalid)
	}
}

func TestNormalizeCleaningRulesRejectsUnboundedOrUnknownDSL(t *testing.T) {
	schema, _ := cleaningTestSchema(t)
	cases := [][]domain.ValidationRule{
		{{Field: "missing", Operation: domain.ValidateRegex, Pattern: "x"}},
		{{Field: "sku", Operation: domain.ValidationOperation("script")}},
	}
	for _, candidate := range cases {
		if _, _, err := NormalizeCleaningRules(nil, candidate, schema); err == nil {
			t.Fatalf("unsafe rule accepted: %+v", candidate)
		}
	}

	tooLong := strings.Repeat("x", MaxSchemaPatternLength+1)
	if _, _, err := NormalizeCleaningRules(nil, []domain.ValidationRule{{Field: "sku",
		Operation: domain.ValidateRegex, Pattern: tooLong}}, schema); err == nil {
		t.Fatal("oversized regex accepted")
	}
}

func TestCleaningIssueOrderIsDeterministicAcrossRetries(t *testing.T) {
	schema, _ := cleaningTestSchema(t)
	input := json.RawMessage(`{"sku":[],"enabled":[],"weight_kg":[],"status":[],"released_on":[],"display_name":[]}`)
	var first string
	for i := 0; i < 50; i++ {
		fields, _, transformIssues, err := TransformRecord(schema, nil, input)
		if err != nil {
			t.Fatal(err)
		}
		_, validationIssues, err := ValidateTransformedRecord(schema, nil, fields)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(append(transformIssues, validationIssues...))
		if i == 0 {
			first = string(raw)
		} else if string(raw) != first {
			t.Fatalf("retry changed issue order:\nfirst=%s\nnow=%s", first, raw)
		}
	}
}

func mustNormalizationRules(t *testing.T, raw string) []domain.NormalizationRule {
	t.Helper()
	var rules []domain.NormalizationRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

func mustValidationRules(t *testing.T, raw string) []domain.ValidationRule {
	t.Helper()
	var rules []domain.ValidationRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}
