package logic

import (
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func TestNormalizeExtractionProfileProducesStableImmutableContract(t *testing.T) {
	schema := model.DatasetSchemaDefinition{ID: "schema-1", SchemaHash: "schema-hash",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string"},"name":{"type":"string"}}}`)}
	input := model.ExtractionProfile{Name: "  产品规格  ", TargetSchemaID: schema.ID,
		RecordGranularity: " 每个产品一条记录 ", SystemInstruction: " 只抽取明确值 ",
		FieldGuides: json.RawMessage(`{"name":"产品名称","sku":"产品编码"}`),
		Examples:    json.RawMessage(`[{"input":"型号 A","records":[{"fields":{"sku":"A"}}]}]`)}
	left, leftHash, err := NormalizeExtractionProfile(input, schema)
	if err != nil {
		t.Fatal(err)
	}
	right, rightHash, err := NormalizeExtractionProfile(input, schema)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash == "" || leftHash != rightHash || string(left.FieldGuides) != string(right.FieldGuides) {
		t.Fatalf("profile contract is not stable: left=%s right=%s", leftHash, rightHash)
	}
	if left.Name != "产品规格" || string(left.NormalizationRules) != "[]" || string(left.ValidationRules) != "[]" {
		t.Fatalf("profile was not normalized: %+v", left)
	}
}

func TestNormalizeExtractionProfileRejectsUnknownOrInactiveConfiguration(t *testing.T) {
	schema := model.DatasetSchemaDefinition{ID: "schema-1", SchemaHash: "schema-hash",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"sku":{"type":"string"}}}`)}
	base := model.ExtractionProfile{Name: "规格", TargetSchemaID: schema.ID,
		RecordGranularity: "每个产品", SystemInstruction: "只抽取原文"}

	cases := []struct {
		name   string
		mutate func(*model.ExtractionProfile)
		want   string
	}{
		{name: "unknown guide field", mutate: func(p *model.ExtractionProfile) {
			p.FieldGuides = json.RawMessage(`{"missing":"x"}`)
		}, want: "不存在的字段"},
		{name: "unknown example key", mutate: func(p *model.ExtractionProfile) {
			p.Examples = json.RawMessage(`[{"input":"x","records":[{"fields":{"sku":"x"}}],"typo":true}]`)
		}, want: "unknown field"},
		{name: "inactive normalization DSL", mutate: func(p *model.ExtractionProfile) {
			p.NormalizationRules = json.RawMessage(`[{"op":"trim"}]`)
		}, want: "尚未启用"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			_, _, err := NormalizeExtractionProfile(candidate, schema)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want substring %q", err, tc.want)
			}
		})
	}
}
