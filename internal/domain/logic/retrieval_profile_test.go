package logic

import (
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func TestNormalizeRetrievalProfileFreezesIndexContract(t *testing.T) {
	schema := model.DatasetSchemaDefinition{ID: "schema-1", SchemaHash: "hash-1",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{
			"title":{"type":"string"},"aliases":{"type":"array","items":{"type":"string"}},
			"priority":{"type":"integer"},"nested":{"type":"object"}
		}}`)}
	profile := model.RetrievalProfile{Name: " Search ", DatasetSchemaID: schema.ID,
		Lexical:      model.LexicalConfig{Fields: map[string]float64{"title": 3, "aliases": 2}},
		Vector:       model.VectorConfig{Fields: []string{"title", "aliases"}, ChunkSize: 100, ChunkOverlap: 20},
		FilterFields: []string{"priority"}, Fusion: model.FusionConfig{}}
	normalized, hash, err := NormalizeRetrievalProfile(profile, schema)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "Search" || normalized.Lexical.Analyzer != "standard" ||
		normalized.Vector.ChunkerVersion != "rune_v1" || normalized.Vector.EmbeddingModel != "platform_default" ||
		normalized.Fusion.Method != "rrf" || normalized.Fusion.RankConstant != 60 || len(hash) != 64 {
		t.Fatalf("规范化结果异常: %+v hash=%s", normalized, hash)
	}
	second, secondHash, err := NormalizeRetrievalProfile(normalized, schema)
	if err != nil || secondHash != hash || second.Vector.ChunkerVersion != "rune_v1" {
		t.Fatalf("Profile 规范化必须幂等: %+v %s err=%v", second, secondHash, err)
	}
}

func TestNormalizeRetrievalProfileRejectsIncompatibleFields(t *testing.T) {
	schema := model.DatasetSchemaDefinition{ID: "schema-1", SchemaHash: "hash-1",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"nested":{"type":"object"}}}`)}
	profile := model.RetrievalProfile{Name: "Search", DatasetSchemaID: schema.ID,
		Lexical: model.LexicalConfig{Fields: map[string]float64{"nested": 1}},
		Vector:  model.VectorConfig{Fields: []string{"title"}, ChunkSize: 100},
		Fusion:  model.FusionConfig{Method: "rrf"}}
	if _, _, err := NormalizeRetrievalProfile(profile, schema); err == nil || !strings.Contains(err.Error(), "string") {
		t.Fatalf("object lexical field 应拒绝: %v", err)
	}
	profile.Lexical.Fields = map[string]float64{"title": 1}
	profile.FilterFields = []string{"nested"}
	if _, _, err := NormalizeRetrievalProfile(profile, schema); err == nil || !strings.Contains(err.Error(), "不可索引") {
		t.Fatalf("object filter field 应拒绝: %v", err)
	}
}
