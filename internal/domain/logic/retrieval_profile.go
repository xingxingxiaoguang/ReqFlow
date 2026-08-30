package logic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"reqflow/internal/domain/model"
)

const (
	MaxRetrievalFields       = 64
	DefaultRetrievalAnalyzer = "standard"
	DefaultRankConstant      = 60
	DefaultCandidateCount    = 100
)

var retrievalAnalyzerPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,62}$`)

type retrievalSchemaField struct {
	Types     map[string]bool
	ItemTypes map[string]bool
}

// NormalizeRetrievalProfile 校验并规范化不可变索引合同。查询级权重、阈值、
// recall limit 和 rerank 参数不属于 Profile，避免业务策略变化触发无意义重建。
func NormalizeRetrievalProfile(profile model.RetrievalProfile, schema model.DatasetSchemaDefinition) (model.RetrievalProfile, string, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" || len(profile.Name) > 200 {
		return profile, "", fmt.Errorf("RetrievalProfile 名称必须为 1..200 字节")
	}
	if profile.DatasetSchemaID == "" || profile.DatasetSchemaID != schema.ID {
		return profile, "", fmt.Errorf("dataset_schema_id 与目标 Schema 不一致")
	}
	fields, err := retrievalSchemaFields(schema.JSONSchema)
	if err != nil {
		return profile, "", err
	}
	if len(profile.Lexical.Fields) == 0 || len(profile.Lexical.Fields) > MaxRetrievalFields {
		return profile, "", fmt.Errorf("lexical.fields 必须包含 1..%d 个字段", MaxRetrievalFields)
	}
	normalizedLexical := make(map[string]float64, len(profile.Lexical.Fields))
	for rawName, weight := range profile.Lexical.Fields {
		name := strings.TrimSpace(rawName)
		field, ok := fields[name]
		if !ok {
			return profile, "", fmt.Errorf("lexical.fields 引用了 Schema 不存在的字段 %s", name)
		}
		if !isTextField(field) {
			return profile, "", fmt.Errorf("lexical 字段 %s 必须是 string 或 string array", name)
		}
		if weight <= 0 || weight > 100 {
			return profile, "", fmt.Errorf("lexical 字段 %s 权重必须在 (0,100]", name)
		}
		normalizedLexical[name] = weight
	}
	profile.Lexical.Fields = normalizedLexical
	profile.Lexical.Analyzer = strings.TrimSpace(profile.Lexical.Analyzer)
	if profile.Lexical.Analyzer == "" {
		profile.Lexical.Analyzer = DefaultRetrievalAnalyzer
	}
	if !retrievalAnalyzerPattern.MatchString(profile.Lexical.Analyzer) {
		return profile, "", fmt.Errorf("lexical.analyzer 必须是合法的 OpenSearch analyzer 标识")
	}

	profile.Vector.Fields, err = normalizeUniqueFieldNames(profile.Vector.Fields, fields, isTextField, "vector.fields")
	if err != nil {
		return profile, "", err
	}
	if len(profile.Vector.Fields) == 0 {
		return profile, "", fmt.Errorf("vector.fields 至少包含一个字段")
	}
	if profile.Vector.ChunkSize <= 0 {
		profile.Vector.ChunkSize = 500
	}
	if profile.Vector.ChunkSize < 32 || profile.Vector.ChunkSize > 8000 {
		return profile, "", fmt.Errorf("vector.chunk_size 必须在 32..8000 之间")
	}
	if profile.Vector.ChunkOverlap < 0 || profile.Vector.ChunkOverlap >= profile.Vector.ChunkSize {
		return profile, "", fmt.Errorf("vector.chunk_overlap 必须大于等于 0 且小于 chunk_size")
	}
	profile.Vector.ChunkerVersion = strings.TrimSpace(profile.Vector.ChunkerVersion)
	if profile.Vector.ChunkerVersion == "" {
		profile.Vector.ChunkerVersion = "rune_v1"
	}
	if profile.Vector.ChunkerVersion != "rune_v1" {
		return profile, "", fmt.Errorf("vector.chunker_version 当前只支持 rune_v1")
	}
	profile.Vector.EmbeddingModel = strings.TrimSpace(profile.Vector.EmbeddingModel)
	if profile.Vector.EmbeddingModel == "" {
		profile.Vector.EmbeddingModel = "platform_default"
	}

	profile.FilterFields, err = normalizeUniqueFieldNames(profile.FilterFields, fields, isFilterField, "filter_fields")
	if err != nil {
		return profile, "", err
	}
	profile.Fusion.Method = strings.ToLower(strings.TrimSpace(profile.Fusion.Method))
	if profile.Fusion.Method == "" {
		profile.Fusion.Method = "rrf"
	}
	if profile.Fusion.Method != "rrf" {
		return profile, "", fmt.Errorf("fusion.method 当前只支持 rrf")
	}
	if profile.Fusion.RankConstant == 0 {
		profile.Fusion.RankConstant = DefaultRankConstant
	}
	if profile.Fusion.RankConstant < 1 || profile.Fusion.RankConstant > 1000 {
		return profile, "", fmt.Errorf("fusion.rank_constant 必须在 1..1000 之间")
	}
	if profile.Fusion.LexicalCandidates == 0 {
		profile.Fusion.LexicalCandidates = DefaultCandidateCount
	}
	if profile.Fusion.VectorCandidates == 0 {
		profile.Fusion.VectorCandidates = DefaultCandidateCount
	}
	if profile.Fusion.LexicalCandidates < 1 || profile.Fusion.LexicalCandidates > 1000 ||
		profile.Fusion.VectorCandidates < 1 || profile.Fusion.VectorCandidates > 1000 {
		return profile, "", fmt.Errorf("fusion 候选数必须在 1..1000 之间")
	}

	payload := struct {
		DatasetSchemaID string              `json:"dataset_schema_id"`
		SchemaHash      string              `json:"schema_hash"`
		Lexical         model.LexicalConfig `json:"lexical"`
		Vector          model.VectorConfig  `json:"vector"`
		FilterFields    []string            `json:"filter_fields"`
		Fusion          model.FusionConfig  `json:"fusion"`
	}{profile.DatasetSchemaID, schema.SchemaHash, profile.Lexical, profile.Vector, profile.FilterFields, profile.Fusion}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	return profile, hex.EncodeToString(digest[:]), nil
}

func retrievalSchemaFields(raw json.RawMessage) (map[string]retrievalSchemaField, error) {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &root); err != nil || len(root.Properties) == 0 {
		return nil, fmt.Errorf("目标 JSON Schema 缺少合法 properties")
	}
	out := make(map[string]retrievalSchemaField, len(root.Properties))
	for name, rawField := range root.Properties {
		var field struct {
			Type  any `json:"type"`
			Items *struct {
				Type any `json:"type"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rawField, &field); err != nil {
			return nil, fmt.Errorf("Schema 字段 %s 非法", name)
		}
		parsed := retrievalSchemaField{Types: schemaTypeSet(field.Type), ItemTypes: map[string]bool{}}
		if field.Items != nil {
			parsed.ItemTypes = schemaTypeSet(field.Items.Type)
		}
		out[name] = parsed
	}
	return out, nil
}

func schemaTypeSet(value any) map[string]bool {
	out := map[string]bool{}
	switch typed := value.(type) {
	case string:
		out[typed] = true
	case []any:
		for _, item := range typed {
			if name, ok := item.(string); ok {
				out[name] = true
			}
		}
	}
	delete(out, "null")
	return out
}

func isTextField(field retrievalSchemaField) bool {
	return field.Types["string"] || (field.Types["array"] && field.ItemTypes["string"])
}

func isFilterField(field retrievalSchemaField) bool {
	for _, typ := range []string{"string", "number", "integer", "boolean"} {
		if field.Types[typ] || (field.Types["array"] && field.ItemTypes[typ]) {
			return true
		}
	}
	return false
}

func normalizeUniqueFieldNames(names []string, fields map[string]retrievalSchemaField,
	compatible func(retrievalSchemaField) bool, label string) ([]string, error) {
	if len(names) > MaxRetrievalFields {
		return nil, fmt.Errorf("%s 最多 %d 个字段", label, MaxRetrievalFields)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		field, ok := fields[name]
		if !ok {
			return nil, fmt.Errorf("%s 引用了 Schema 不存在的字段 %s", label, name)
		}
		if !compatible(field) {
			return nil, fmt.Errorf("%s 的字段 %s 类型不可索引", label, name)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}
