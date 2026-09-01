package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

var supportedSchemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true,
	"items": true, "enum": true, "minimum": true, "maximum": true,
	"minLength": true, "maxLength": true, "pattern": true, "description": true,
	"title": true, "default": true,
}

func ValidateCapabilityConfig(definition CapabilityDefinition, raw json.RawMessage) error {
	config := raw
	if len(bytes.TrimSpace(config)) == 0 {
		config = definition.DefaultConfig
	}
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage(`{}`)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("节点配置不是合法 JSON: %w", err)
	}
	if len(bytes.TrimSpace(definition.ConfigSchema)) == 0 {
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("节点配置必须是 JSON object")
		}
		return nil
	}
	var schema map[string]any
	if err := decodeSchema(definition.ConfigSchema, &schema); err != nil {
		return fmt.Errorf("Capability 配置 Schema 非法: %w", err)
	}
	if err := validateSchemaValue(schema, value, "config"); err != nil {
		return err
	}
	return nil
}

func validateConfigSchema(raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fmt.Errorf("config_schema 不能为空")
	}
	var schema map[string]any
	if err := decodeSchema(raw, &schema); err != nil {
		return err
	}
	if schema["type"] != "object" {
		return fmt.Errorf("config_schema 根节点 type 必须是 object")
	}
	additional, exists := schema["additionalProperties"]
	if !exists || additional != false {
		return fmt.Errorf("config_schema 根节点必须声明 additionalProperties=false")
	}
	return validateSchemaNode(schema, "config_schema")
}

func decodeSchema(raw json.RawMessage, target *map[string]any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || *target == nil {
		return fmt.Errorf("必须是 JSON object")
	}
	return nil
}

func validateSchemaNode(schema map[string]any, path string) error {
	for keyword := range schema {
		if !supportedSchemaKeywords[keyword] {
			return fmt.Errorf("%s 包含不支持的关键字 %s", path, keyword)
		}
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		if additional, exists := schema["additionalProperties"]; exists && additional != false {
			return fmt.Errorf("%s additionalProperties 只能是 false", path)
		}
		properties, _ := schema["properties"].(map[string]any)
		for name, rawChild := range properties {
			child, ok := rawChild.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s 必须是 Schema object", path, name)
			}
			if err := validateSchemaNode(child, path+".properties."+name); err != nil {
				return err
			}
		}
		if rawRequired, exists := schema["required"]; exists {
			required, ok := rawRequired.([]any)
			if !ok {
				return fmt.Errorf("%s.required 必须是字符串数组", path)
			}
			seen := map[string]bool{}
			for _, item := range required {
				name, ok := item.(string)
				if !ok || name == "" || seen[name] {
					return fmt.Errorf("%s.required 包含非法或重复字段", path)
				}
				if _, exists := properties[name]; !exists {
					return fmt.Errorf("%s.required 引用了未声明字段 %s", path, name)
				}
				seen[name] = true
			}
		}
	case "array":
		rawItems, ok := schema["items"]
		if !ok {
			return fmt.Errorf("%s 数组 Schema 必须声明 items", path)
		}
		items, ok := rawItems.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.items 必须是 Schema object", path)
		}
		return validateSchemaNode(items, path+".items")
	case "string", "integer", "number", "boolean":
	case "":
		return fmt.Errorf("%s 缺少 type", path)
	default:
		return fmt.Errorf("%s 使用了不支持的 type %q", path, typeName)
	}
	if pattern, exists := schema["pattern"]; exists {
		text, ok := pattern.(string)
		if !ok {
			return fmt.Errorf("%s.pattern 必须是字符串", path)
		}
		if _, err := regexp.Compile(text); err != nil {
			return fmt.Errorf("%s.pattern 非法", path)
		}
	}
	if rawDefault, exists := schema["default"]; exists {
		if err := validateSchemaValue(schema, rawDefault, path+".default"); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	if enum, exists := schema["enum"].([]any); exists {
		matched := false
		for _, candidate := range enum {
			if fmt.Sprint(candidate) == fmt.Sprint(value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s 不在允许的枚举值中", path)
		}
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 必须是 object", path)
		}
		properties, _ := schema["properties"].(map[string]any)
		if rawRequired, ok := schema["required"].([]any); ok {
			for _, item := range rawRequired {
				name := item.(string)
				if _, exists := object[name]; !exists {
					return fmt.Errorf("%s.%s 是必填字段", path, name)
				}
			}
		}
		for name, childValue := range object {
			rawChild, exists := properties[name]
			if !exists {
				if schema["additionalProperties"] == false {
					return fmt.Errorf("%s.%s 是未声明字段", path, name)
				}
				continue
			}
			if err := validateSchemaValue(rawChild.(map[string]any), childValue, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s 必须是 array", path)
		}
		items := schema["items"].(map[string]any)
		for index, item := range array {
			if err := validateSchemaValue(items, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s 必须是 string", path)
		}
		if minimum, ok := schemaInteger(schema["minLength"]); ok && len([]rune(text)) < minimum {
			return fmt.Errorf("%s 长度不能小于 %d", path, minimum)
		}
		if maximum, ok := schemaInteger(schema["maxLength"]); ok && len([]rune(text)) > maximum {
			return fmt.Errorf("%s 长度不能大于 %d", path, maximum)
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(text) {
			return fmt.Errorf("%s 不符合 pattern", path)
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return fmt.Errorf("%s 必须是 integer", path)
		}
		parsed, err := number.Int64()
		if err != nil {
			return fmt.Errorf("%s 必须是 integer", path)
		}
		if err := validateNumberBounds(schema, float64(parsed), path); err != nil {
			return err
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s 必须是 number", path)
		}
		parsed, err := number.Float64()
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return fmt.Errorf("%s 必须是有限 number", path)
		}
		if err := validateNumberBounds(schema, parsed, path); err != nil {
			return err
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s 必须是 boolean", path)
		}
	default:
		return fmt.Errorf("%s 对应的 Schema type 非法", path)
	}
	return nil
}

func validateNumberBounds(schema map[string]any, value float64, path string) error {
	if minimum, ok := schemaNumber(schema["minimum"]); ok && value < minimum {
		return fmt.Errorf("%s 不能小于 %v", path, minimum)
	}
	if maximum, ok := schemaNumber(schema["maximum"]); ok && value > maximum {
		return fmt.Errorf("%s 不能大于 %v", path, maximum)
	}
	return nil
}

func schemaNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil
}

func schemaInteger(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Int64()
	return int(parsed), err == nil && parsed >= 0
}
