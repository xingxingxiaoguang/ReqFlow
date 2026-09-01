package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CompileDataContract turns the authored field contract into the immutable
// JSON Schema consumed by extraction, transformation and validation.
func CompileDataContract(contract DataContract) (json.RawMessage, string, error) {
	return compileFieldSchema(contract.Fields)
}

// CompileOutputContract produces the output schema used by analysis agents and
// human completion validation.
func CompileOutputContract(contract OutputContract) (json.RawMessage, string, error) {
	return compileFieldSchema(contract.Fields)
}

func HashContract(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func compileFieldSchema(fields []FieldContract) (json.RawMessage, string, error) {
	if len(fields) == 0 {
		return nil, "", fmt.Errorf("字段合同不能为空")
	}
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		compiled, err := compileField(field)
		if err != nil {
			return nil, "", fmt.Errorf("字段 %s: %w", field.Key, err)
		}
		if _, exists := properties[field.Key]; exists {
			return nil, "", fmt.Errorf("字段 %s 重复", field.Key)
		}
		properties[field.Key] = compiled
		if field.Required {
			required = append(required, field.Key)
		}
	}
	root := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		root["required"] = required
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return raw, hex.EncodeToString(digest[:]), nil
}

func compileField(field FieldContract) (map[string]any, error) {
	if field.Key == "" {
		return nil, fmt.Errorf("key 不能为空")
	}
	result := map[string]any{}
	if field.Label != "" {
		result["title"] = field.Label
	}
	if field.Description != "" {
		result["description"] = field.Description
	}
	switch field.Type {
	case FieldString:
		result["type"] = "string"
	case FieldInteger:
		result["type"] = "integer"
	case FieldNumber:
		result["type"] = "number"
	case FieldBoolean:
		result["type"] = "boolean"
	case FieldDate:
		result["type"], result["format"] = "string", "date"
	case FieldDateTime:
		result["type"], result["format"] = "string", "date-time"
	case FieldObject:
		properties := make(map[string]any, len(field.Properties))
		required := make([]string, 0, len(field.Properties))
		for _, child := range field.Properties {
			compiled, err := compileField(child)
			if err != nil {
				return nil, err
			}
			if _, exists := properties[child.Key]; exists {
				return nil, fmt.Errorf("嵌套字段 %s 重复", child.Key)
			}
			properties[child.Key] = compiled
			if child.Required {
				required = append(required, child.Key)
			}
		}
		result["type"], result["properties"], result["additionalProperties"] = "object", properties, false
		if len(required) > 0 {
			result["required"] = required
		}
	case FieldArray:
		if field.Items == nil {
			return nil, fmt.Errorf("array 必须声明 items")
		}
		items, err := compileField(*field.Items)
		if err != nil {
			return nil, err
		}
		delete(items, "title")
		result["type"], result["items"] = "array", items
	default:
		return nil, fmt.Errorf("不支持的类型 %q", field.Type)
	}
	if len(field.Enum) > 0 {
		result["enum"] = field.Enum
	}
	return result, nil
}
