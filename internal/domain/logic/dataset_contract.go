package logic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	MaxDatasetSchemaBytes  = 256 << 10
	MaxUISchemaBytes       = 128 << 10
	MaxDatasetSchemaDepth  = 8
	MaxDatasetSchemaFields = 256
	MaxSchemaEnumValues    = 200
	MaxSchemaPatternLength = 512
)

var supportedSchemaTypes = map[string]bool{
	"string": true, "integer": true, "number": true,
	"boolean": true, "array": true, "object": true,
}

var supportedSchemaKeywords = map[string]bool{
	"$schema": true, "title": true, "description": true,
	"type": true, "properties": true, "required": true,
	"additionalProperties": true, "items": true, "enum": true,
	"format": true, "minimum": true, "maximum": true, "pattern": true,
	"minItems": true, "maxItems": true, "minLength": true, "maxLength": true,
}

// NormalizeDatasetSchema 验证平台支持的 JSON Schema 子集，补齐对象的
// additionalProperties=false，并返回稳定 JSON 与结构哈希。
func NormalizeDatasetSchema(payload json.RawMessage) (json.RawMessage, string, error) {
	if len(payload) == 0 {
		return nil, "", fmt.Errorf("JSON Schema 不能为空")
	}
	if len(payload) > MaxDatasetSchemaBytes {
		return nil, "", fmt.Errorf("JSON Schema 超过 %d 字节", MaxDatasetSchemaBytes)
	}

	var root map[string]any
	if err := decodeSingleJSON(payload, &root); err != nil {
		return nil, "", fmt.Errorf("JSON Schema 非法: %w", err)
	}
	if root == nil {
		return nil, "", fmt.Errorf("JSON Schema 根节点必须是 object")
	}

	fieldCount := 0
	if err := normalizeSchemaNode(root, "$", 1, true, &fieldCount); err != nil {
		return nil, "", err
	}
	canonical, err := json.Marshal(root)
	if err != nil {
		return nil, "", fmt.Errorf("序列化 JSON Schema: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

// NormalizeUISchema 只接受 JSON object，并返回稳定 JSON。它不参与结构兼容判断。
func NormalizeUISchema(payload json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(payload) > MaxUISchemaBytes {
		return nil, fmt.Errorf("UI Schema 超过 %d 字节", MaxUISchemaBytes)
	}
	var root map[string]any
	if err := decodeSingleJSON(payload, &root); err != nil {
		return nil, fmt.Errorf("UI Schema 非法: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("UI Schema 根节点必须是 object")
	}
	return json.Marshal(root)
}

func decodeSingleJSON(payload []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("只能包含一个 JSON 值")
		}
		return err
	}
	return nil
}

func normalizeSchemaNode(node map[string]any, path string, depth int, root bool, fieldCount *int) error {
	if depth > MaxDatasetSchemaDepth {
		return fmt.Errorf("字段 %s 的嵌套深度超过 %d", path, MaxDatasetSchemaDepth)
	}
	for key := range node {
		if !supportedSchemaKeywords[key] {
			return fmt.Errorf("字段 %s 使用了暂不支持的 JSON Schema 关键字 %q", path, key)
		}
	}

	typ, ok := node["type"].(string)
	if !ok || !supportedSchemaTypes[typ] {
		return fmt.Errorf("字段 %s 必须声明受支持的 type", path)
	}
	if root && typ != "object" {
		return fmt.Errorf("JSON Schema 根节点 type 必须为 object")
	}

	if enum, exists := node["enum"]; exists {
		values, ok := enum.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("字段 %s 的 enum 必须是非空数组", path)
		}
		if len(values) > MaxSchemaEnumValues {
			return fmt.Errorf("字段 %s 的 enum 超过 %d 项", path, MaxSchemaEnumValues)
		}
	}
	if pattern, exists := node["pattern"]; exists {
		p, ok := pattern.(string)
		if !ok {
			return fmt.Errorf("字段 %s 的 pattern 必须是字符串", path)
		}
		if len(p) > MaxSchemaPatternLength {
			return fmt.Errorf("字段 %s 的 pattern 超过 %d 字节", path, MaxSchemaPatternLength)
		}
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("字段 %s 的 pattern 非法: %w", path, err)
		}
	}

	switch typ {
	case "object":
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("字段 %s 的 object 必须声明 properties", path)
		}
		if root && len(props) == 0 {
			return fmt.Errorf("JSON Schema 至少需要一个字段")
		}
		for name, raw := range props {
			if !IsValidIdentifier(name) {
				return fmt.Errorf("字段 %s.%s 名称非法，必须为 snake_case", path, name)
			}
			child, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("字段 %s.%s 定义必须是 object", path, name)
			}
			*fieldCount++
			if *fieldCount > MaxDatasetSchemaFields {
				return fmt.Errorf("字段总数超过 %d", MaxDatasetSchemaFields)
			}
			if err := normalizeSchemaNode(child, path+"."+name, depth+1, false, fieldCount); err != nil {
				return err
			}
		}
		if err := validateRequired(node["required"], props, path); err != nil {
			return err
		}
		if additional, exists := node["additionalProperties"]; exists {
			allowed, ok := additional.(bool)
			if !ok || allowed {
				return fmt.Errorf("字段 %s 的 additionalProperties 必须为 false", path)
			}
		} else {
			node["additionalProperties"] = false
		}
	case "array":
		items, ok := node["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("字段 %s 的 array 必须声明单一 items Schema", path)
		}
		if err := normalizeSchemaNode(items, path+"[]", depth+1, false, fieldCount); err != nil {
			return err
		}
	default:
		if _, exists := node["properties"]; exists {
			return fmt.Errorf("非 object 字段 %s 不能声明 properties", path)
		}
		if _, exists := node["items"]; exists {
			return fmt.Errorf("非 array 字段 %s 不能声明 items", path)
		}
	}
	return nil
}

func validateRequired(raw any, props map[string]any, path string) error {
	if raw == nil {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("字段 %s 的 required 必须是字符串数组", path)
	}
	seen := map[string]bool{}
	for _, value := range values {
		name, ok := value.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return fmt.Errorf("字段 %s 的 required 必须是字符串数组", path)
		}
		if _, exists := props[name]; !exists {
			return fmt.Errorf("字段 %s 的 required 引用了不存在的属性 %q", path, name)
		}
		if seen[name] {
			return fmt.Errorf("字段 %s 的 required 重复声明 %q", path, name)
		}
		seen[name] = true
	}
	return nil
}

// NextCommitRange 计算追加 Batch 的连续位点；空 Batch 不允许提交。
func NextCommitRange(currentSeq int64, itemCount int) (from, to int64, err error) {
	if currentSeq < 0 {
		return 0, 0, fmt.Errorf("current_seq 不能为负数")
	}
	if itemCount <= 0 {
		return 0, 0, fmt.Errorf("不能提交空 Batch")
	}
	from = currentSeq + 1
	to = currentSeq + int64(itemCount)
	if to < from {
		return 0, 0, fmt.Errorf("commit_seq 溢出")
	}
	return from, to, nil
}

// ValidateDatasetKeyFields 校验 Dataset 的业务主键字段。首期只允许 1..4 个标量字段。
func ValidateDatasetKeyFields(schemaPayload json.RawMessage, keyFields []string) error {
	if len(keyFields) == 0 || len(keyFields) > 4 {
		return fmt.Errorf("key_fields 必须包含 1..4 个字段")
	}
	var root map[string]any
	if err := decodeSingleJSON(schemaPayload, &root); err != nil {
		return fmt.Errorf("JSON Schema 非法: %w", err)
	}
	props, _ := root["properties"].(map[string]any)
	seen := map[string]bool{}
	for _, field := range keyFields {
		if seen[field] {
			return fmt.Errorf("key_fields 重复声明 %s", field)
		}
		seen[field] = true
		raw, exists := props[field]
		if !exists {
			return fmt.Errorf("key_fields 引用了不存在的字段 %s", field)
		}
		node, _ := raw.(map[string]any)
		typ, _ := node["type"].(string)
		if typ == "object" || typ == "array" || typ == "" {
			return fmt.Errorf("key_fields 字段 %s 必须是标量类型", field)
		}
	}
	return nil
}

// NormalizeDatasetItem 按已规范化的 Dataset Schema 校验并稳定序列化字段袋。
func NormalizeDatasetItem(schemaPayload, fields json.RawMessage) (json.RawMessage, error) {
	var schema map[string]any
	if err := decodeSingleJSON(schemaPayload, &schema); err != nil {
		return nil, fmt.Errorf("JSON Schema 非法: %w", err)
	}
	var values map[string]any
	if err := decodeSingleJSON(fields, &values); err != nil {
		return nil, fmt.Errorf("字段值不是合法 JSON object: %w", err)
	}
	if values == nil {
		return nil, fmt.Errorf("字段值根节点必须是 object")
	}
	if err := validateJSONValue(schema, values, "$"); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("序列化字段值: %w", err)
	}
	return canonical, nil
}

// DatasetItemIdentity 生成稳定 item_key 和 fingerprint。fields 必须已通过
// NormalizeDatasetItem；fingerprint 额外加入 SchemaHash，避免结构合同变化后误复用。
func DatasetItemIdentity(schemaHash string, keyFields []string, fields json.RawMessage) (itemKey, fingerprint string, err error) {
	var values map[string]any
	if err := decodeSingleJSON(fields, &values); err != nil {
		return "", "", fmt.Errorf("字段值非法: %w", err)
	}
	keyValues := make([]any, 0, len(keyFields))
	for _, field := range keyFields {
		value, exists := values[field]
		if !exists || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", "", fmt.Errorf("主键字段 %s 不能为空", field)
		}
		keyValues = append(keyValues, value)
	}
	keyJSON, _ := json.Marshal(keyValues)
	keySum := sha256.Sum256(keyJSON)
	fingerprintSum := sha256.Sum256(append([]byte(schemaHash+"\n"), fields...))
	return hex.EncodeToString(keySum[:]), hex.EncodeToString(fingerprintSum[:]), nil
}

func validateJSONValue(schema map[string]any, value any, path string) error {
	typ, _ := schema["type"].(string)
	if value == nil {
		return fmt.Errorf("字段 %s 不允许为 null", path)
	}
	if enum, exists := schema["enum"].([]any); exists {
		matched := false
		actual, _ := json.Marshal(value)
		for _, allowed := range enum {
			candidate, _ := json.Marshal(allowed)
			if bytes.Equal(actual, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("字段 %s 的值不在 enum 范围内", path)
		}
	}

	switch typ {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("字段 %s 必须是 object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		required := map[string]bool{}
		if raw, ok := schema["required"].([]any); ok {
			for _, entry := range raw {
				if name, ok := entry.(string); ok {
					required[name] = true
				}
			}
		}
		requiredNames := make([]string, 0, len(required))
		for name := range required {
			requiredNames = append(requiredNames, name)
		}
		sort.Strings(requiredNames)
		for _, name := range requiredNames {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("字段 %s.%s 为必填项", path, name)
			}
		}
		objectFields := make([]string, 0, len(object))
		for name := range object {
			objectFields = append(objectFields, name)
		}
		sort.Strings(objectFields)
		for _, name := range objectFields {
			childValue := object[name]
			raw, exists := props[name]
			if !exists {
				return fmt.Errorf("字段 %s.%s 未在 Schema 中声明", path, name)
			}
			child, _ := raw.(map[string]any)
			if err := validateJSONValue(child, childValue, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("字段 %s 必须是 array", path)
		}
		if err := validateLength(schema, len(array), path, "minItems", "maxItems"); err != nil {
			return err
		}
		items, _ := schema["items"].(map[string]any)
		for i, entry := range array {
			if err := validateJSONValue(items, entry, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("字段 %s 必须是 string", path)
		}
		if err := validateLength(schema, len([]rune(text)), path, "minLength", "maxLength"); err != nil {
			return err
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(text) {
			return fmt.Errorf("字段 %s 不符合 pattern", path)
		}
		if format, ok := schema["format"].(string); ok {
			if err := validateStringFormat(format, text); err != nil {
				return fmt.Errorf("字段 %s: %w", path, err)
			}
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("字段 %s 必须是 integer", path)
		}
		if _, err := strconv.ParseInt(string(number), 10, 64); err != nil {
			return fmt.Errorf("字段 %s 必须是 integer", path)
		}
		if err := validateNumberRange(schema, number, path); err != nil {
			return err
		}
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("字段 %s 必须是 number", path)
		}
		if err := validateNumberRange(schema, number, path); err != nil {
			return err
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("字段 %s 必须是 boolean", path)
		}
	default:
		return fmt.Errorf("字段 %s 的 Schema type 非法", path)
	}
	return nil
}

func validateLength(schema map[string]any, actual int, path, minKey, maxKey string) error {
	if min, ok := jsonNumberAsInt(schema[minKey]); ok && actual < min {
		return fmt.Errorf("字段 %s 长度不能小于 %d", path, min)
	}
	if max, ok := jsonNumberAsInt(schema[maxKey]); ok && actual > max {
		return fmt.Errorf("字段 %s 长度不能大于 %d", path, max)
	}
	return nil
}

func jsonNumberAsInt(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(string(number))
	return n, err == nil
}

func validateNumberRange(schema map[string]any, value json.Number, path string) error {
	n, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return fmt.Errorf("字段 %s 必须是有限数字", path)
	}
	if min, ok := jsonNumberAsFloat(schema["minimum"]); ok && n < min {
		return fmt.Errorf("字段 %s 不能小于 %v", path, min)
	}
	if max, ok := jsonNumberAsFloat(schema["maximum"]); ok && n > max {
		return fmt.Errorf("字段 %s 不能大于 %v", path, max)
	}
	return nil
}

func jsonNumberAsFloat(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseFloat(string(number), 64)
	return n, err == nil
}

func validateStringFormat(format, value string) error {
	switch format {
	case "date":
		if _, err := time.Parse("2006-01-02", value); err != nil {
			return fmt.Errorf("必须是 ISO 8601 date")
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("必须是 RFC3339 date-time")
		}
	case "", "email", "uri":
		// email/uri 首期只保留格式声明，不在领域层做不完整的协议校验。
	default:
		return fmt.Errorf("暂不支持 format %q", format)
	}
	return nil
}
