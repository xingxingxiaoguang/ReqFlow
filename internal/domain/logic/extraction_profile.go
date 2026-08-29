package logic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"reqflow/internal/domain/model"
)

const (
	MaxExtractionInstructionBytes = 32 << 10
	MaxExtractionProfileJSONBytes = 256 << 10
)

// NormalizeExtractionProfile 校验并规范化不可变抽取配置。NormalizationRules 和
// ValidationRules 在对应确定性 Executor 的 DSL 落地前只允许空数组，防止先存入
// 一批运行时实际不会执行的“配置”。
func NormalizeExtractionProfile(profile model.ExtractionProfile, schema model.DatasetSchemaDefinition) (model.ExtractionProfile, string, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.RecordGranularity = strings.TrimSpace(profile.RecordGranularity)
	profile.SystemInstruction = strings.TrimSpace(profile.SystemInstruction)
	if profile.Name == "" || len(profile.Name) > 200 {
		return profile, "", fmt.Errorf("ExtractionProfile 名称必须为 1..200 字节")
	}
	if profile.TargetSchemaID == "" || profile.TargetSchemaID != schema.ID {
		return profile, "", fmt.Errorf("target_schema_id 与目标 Schema 不一致")
	}
	if profile.RecordGranularity == "" || len(profile.RecordGranularity) > 500 {
		return profile, "", fmt.Errorf("record_granularity 必须为 1..500 字节")
	}
	if profile.SystemInstruction == "" || len(profile.SystemInstruction) > MaxExtractionInstructionBytes {
		return profile, "", fmt.Errorf("system_instruction 必须为 1..%d 字节", MaxExtractionInstructionBytes)
	}
	fields, err := schemaRootFields(schema.JSONSchema)
	if err != nil {
		return profile, "", err
	}
	profile.FieldGuides, err = normalizeFieldGuides(profile.FieldGuides, fields)
	if err != nil {
		return profile, "", err
	}
	profile.Examples, err = normalizeExtractionExamples(profile.Examples, fields)
	if err != nil {
		return profile, "", err
	}
	profile.NormalizationRules, err = normalizeReservedRules("normalization_rules", profile.NormalizationRules)
	if err != nil {
		return profile, "", err
	}
	profile.ValidationRules, err = normalizeReservedRules("validation_rules", profile.ValidationRules)
	if err != nil {
		return profile, "", err
	}
	payload := struct {
		TargetSchemaID     string          `json:"target_schema_id"`
		TargetSchemaHash   string          `json:"target_schema_hash"`
		RecordGranularity  string          `json:"record_granularity"`
		SystemInstruction  string          `json:"system_instruction"`
		FieldGuides        json.RawMessage `json:"field_guides"`
		Examples           json.RawMessage `json:"examples"`
		NormalizationRules json.RawMessage `json:"normalization_rules"`
		ValidationRules    json.RawMessage `json:"validation_rules"`
	}{profile.TargetSchemaID, schema.SchemaHash, profile.RecordGranularity,
		profile.SystemInstruction, profile.FieldGuides, profile.Examples,
		profile.NormalizationRules, profile.ValidationRules}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	return profile, hex.EncodeToString(digest[:]), nil
}

func schemaRootFields(payload json.RawMessage) (map[string]struct{}, error) {
	var root map[string]any
	if err := decodeSingleJSON(payload, &root); err != nil {
		return nil, fmt.Errorf("目标 JSON Schema 非法: %w", err)
	}
	properties, ok := root["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return nil, fmt.Errorf("目标 JSON Schema 缺少 properties")
	}
	fields := make(map[string]struct{}, len(properties))
	for name := range properties {
		fields[name] = struct{}{}
	}
	return fields, nil
}

func normalizeFieldGuides(raw json.RawMessage, fields map[string]struct{}) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > MaxExtractionProfileJSONBytes {
		return nil, fmt.Errorf("field_guides 超过大小限制")
	}
	var guides map[string]string
	if err := decodeSingleJSON(raw, &guides); err != nil || guides == nil {
		return nil, fmt.Errorf("field_guides 必须是字段名到非空说明的 JSON object")
	}
	for field, guide := range guides {
		if _, ok := fields[field]; !ok {
			return nil, fmt.Errorf("field_guides 引用了目标 Schema 不存在的字段 %s", field)
		}
		guide = strings.TrimSpace(guide)
		if guide == "" || len(guide) > 4000 {
			return nil, fmt.Errorf("字段 %s 的提取说明必须为 1..4000 字节", field)
		}
		guides[field] = guide
	}
	return json.Marshal(guides)
}

func normalizeExtractionExamples(raw json.RawMessage, fields map[string]struct{}) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`[]`), nil
	}
	if len(raw) > MaxExtractionProfileJSONBytes {
		return nil, fmt.Errorf("examples 超过大小限制")
	}
	var examples []struct {
		Input   string `json:"input"`
		Records []struct {
			Fields map[string]any `json:"fields"`
		} `json:"records"`
	}
	if err := decodeStrictSingleJSON(raw, &examples); err != nil {
		return nil, fmt.Errorf("examples 必须是 [{input,records:[{fields}]}] 数组: %w", err)
	}
	if len(examples) > 20 {
		return nil, fmt.Errorf("examples 最多 20 组")
	}
	for i, example := range examples {
		if strings.TrimSpace(example.Input) == "" || len(example.Records) == 0 {
			return nil, fmt.Errorf("第 %d 组 example 必须包含 input 和至少一条 record", i+1)
		}
		if len(example.Records) > 100 {
			return nil, fmt.Errorf("第 %d 组 example 最多包含 100 条 record", i+1)
		}
		for _, record := range example.Records {
			if len(record.Fields) == 0 {
				return nil, fmt.Errorf("第 %d 组 example 的 fields 不能为空", i+1)
			}
			for field := range record.Fields {
				if _, ok := fields[field]; !ok {
					return nil, fmt.Errorf("第 %d 组 example 引用了不存在字段 %s", i+1, field)
				}
			}
		}
	}
	return json.Marshal(examples)
}

func decodeStrictSingleJSON(payload []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("只能包含一个 JSON 值")
		}
		return err
	}
	return nil
}

func normalizeReservedRules(name string, raw json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`[]`), nil
	}
	if len(raw) > MaxExtractionProfileJSONBytes {
		return nil, fmt.Errorf("%s 超过大小限制", name)
	}
	var rules []any
	if err := decodeSingleJSON(raw, &rules); err != nil {
		return nil, fmt.Errorf("%s 必须是 JSON array: %w", name, err)
	}
	if len(rules) != 0 {
		return nil, fmt.Errorf("%s 的声明式 DSL 尚未启用，当前必须为空数组", name)
	}
	return json.RawMessage(`[]`), nil
}
