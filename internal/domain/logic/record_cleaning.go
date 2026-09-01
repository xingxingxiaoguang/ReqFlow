package logic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
)

const MaxCleaningRulesJSONBytes = 256 << 10

const (
	MaxCleaningRules       = 128
	MaxCleaningRuleValues  = 200
	MaxCleaningRuleMessage = 1000
)

var unitValuePattern = regexp.MustCompile(`^([-+]?(?:\d+(?:\.\d*)?|\.\d+))\s*(\S+)$`)

// NormalizeCleaningRules 校验并规范化 Workflow 的受控转换/校验 DSL。规则只能引用
// 目标 Schema 顶层字段；不接受表达式、脚本或任意函数名。
func NormalizeCleaningRules(normalization []domain.NormalizationRule, validation []domain.ValidationRule,
	schemaPayload json.RawMessage) ([]domain.NormalizationRule, []domain.ValidationRule, error) {
	properties, err := rootSchemaProperties(schemaPayload)
	if err != nil {
		return nil, nil, err
	}
	normalization, err = normalizeNormalizationRules(normalization, properties)
	if err != nil {
		return nil, nil, err
	}
	validation, err = normalizeValidationRules(validation, properties)
	if err != nil {
		return nil, nil, err
	}
	return normalization, validation, nil
}

func normalizeNormalizationRules(source []domain.NormalizationRule, properties map[string]any) ([]domain.NormalizationRule, error) {
	if len(source) == 0 {
		return []domain.NormalizationRule{}, nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxCleaningRulesJSONBytes {
		return nil, fmt.Errorf("normalization_rules 超过大小限制")
	}
	rules := cloneNormalizationRules(source)
	if len(rules) > MaxCleaningRules {
		return nil, fmt.Errorf("normalization_rules 最多 %d 条", MaxCleaningRules)
	}
	seen := make(map[string]bool, len(rules))
	for i := range rules {
		rule := &rules[i]
		rule.Field = strings.TrimSpace(rule.Field)
		rule.Operation = domain.NormalizationOperation(strings.TrimSpace(string(rule.Operation)))
		node, ok := properties[rule.Field].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("normalization_rules 第 %d 条引用了不存在的字段 %s", i+1, rule.Field)
		}
		if seen[rule.Field] {
			return nil, fmt.Errorf("字段 %s 只能声明一条 normalization rule", rule.Field)
		}
		seen[rule.Field] = true
		if err := validateNormalizationRule(rule, node, properties); err != nil {
			return nil, fmt.Errorf("normalization_rules 第 %d 条: %w", i+1, err)
		}
	}
	return rules, nil
}

func cloneNormalizationRules(source []domain.NormalizationRule) []domain.NormalizationRule {
	rules := append([]domain.NormalizationRule(nil), source...)
	for index := range rules {
		rules[index].Aliases = cloneAnyMap(rules[index].Aliases)
		rules[index].TrueValues = append([]string(nil), rules[index].TrueValues...)
		rules[index].FalseValues = append([]string(nil), rules[index].FalseValues...)
		rules[index].Layouts = append([]string(nil), rules[index].Layouts...)
		rules[index].Units = cloneFloatMap(rules[index].Units)
		rules[index].SourceFields = append([]string(nil), rules[index].SourceFields...)
	}
	return rules
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func validateNormalizationRule(rule *domain.NormalizationRule, node map[string]any, properties map[string]any) error {
	typ, _ := node["type"].(string)
	switch rule.Operation {
	case "enum_alias":
		if err := rejectUnusedNormalizationParams(rule, "aliases"); err != nil {
			return err
		}
		if len(rule.Aliases) == 0 || len(rule.Aliases) > MaxCleaningRuleValues {
			return fmt.Errorf("enum_alias aliases 必须包含 1..%d 项", MaxCleaningRuleValues)
		}
		normalized := make(map[string]any, len(rule.Aliases))
		for alias, value := range rule.Aliases {
			key := normalizeText(alias)
			if key == "" {
				return fmt.Errorf("enum_alias 不能包含空别名")
			}
			if _, exists := normalized[key]; exists {
				return fmt.Errorf("enum_alias 规范化后别名重复: %s", alias)
			}
			if err := validateJSONValue(node, normalizeLooseValue(value), "$."+rule.Field); err != nil {
				return fmt.Errorf("enum_alias %s 的目标值不符合 Schema: %w", alias, err)
			}
			normalized[key] = normalizeLooseValue(value)
		}
		rule.Aliases = normalized
	case "boolean_alias":
		if err := rejectUnusedNormalizationParams(rule, "true_values", "false_values"); err != nil {
			return err
		}
		if typ != "boolean" {
			return fmt.Errorf("boolean_alias 只能用于 boolean 字段")
		}
		if len(rule.TrueValues) == 0 || len(rule.FalseValues) == 0 ||
			len(rule.TrueValues)+len(rule.FalseValues) > MaxCleaningRuleValues {
			return fmt.Errorf("boolean_alias 必须同时提供非空 true_values 和 false_values")
		}
		truthy := normalizeStringSet(rule.TrueValues)
		falsy := normalizeStringSet(rule.FalseValues)
		if len(truthy) == 0 || len(falsy) == 0 {
			return fmt.Errorf("boolean_alias 规范化后的 true_values/false_values 不能为空")
		}
		for value := range truthy {
			if falsy[value] {
				return fmt.Errorf("boolean_alias 值 %s 同时属于 true/false", value)
			}
		}
		rule.TrueValues, rule.FalseValues = sortedSet(truthy), sortedSet(falsy)
	case "date":
		if err := rejectUnusedNormalizationParams(rule, "layouts"); err != nil {
			return err
		}
		format, _ := node["format"].(string)
		if typ != "string" || (format != "date" && format != "date-time") {
			return fmt.Errorf("date 只能用于 format=date/date-time 的 string 字段")
		}
		if len(rule.Layouts) == 0 || len(rule.Layouts) > 16 {
			return fmt.Errorf("date layouts 必须包含 1..16 项")
		}
		seen := map[string]bool{}
		for j, layout := range rule.Layouts {
			layout = strings.TrimSpace(layout)
			if layout == "" || len(layout) > 100 || seen[layout] {
				return fmt.Errorf("date layout 为空、重复或过长")
			}
			seen[layout], rule.Layouts[j] = true, layout
		}
	case "unit_scale":
		if err := rejectUnusedNormalizationParams(rule, "units"); err != nil {
			return err
		}
		if typ != "integer" && typ != "number" {
			return fmt.Errorf("unit_scale 只能用于 integer/number 字段")
		}
		if len(rule.Units) == 0 || len(rule.Units) > MaxCleaningRuleValues {
			return fmt.Errorf("unit_scale units 必须包含 1..%d 项", MaxCleaningRuleValues)
		}
		normalized := make(map[string]float64, len(rule.Units))
		for unit, factor := range rule.Units {
			key := strings.ToLower(normalizeText(unit))
			if key == "" || factor == 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
				return fmt.Errorf("unit_scale 单位或换算系数非法")
			}
			if _, exists := normalized[key]; exists {
				return fmt.Errorf("unit_scale 规范化后单位重复: %s", unit)
			}
			normalized[key] = factor
		}
		rule.Units = normalized
	case "split":
		if err := rejectUnusedNormalizationParams(rule, "separator"); err != nil {
			return err
		}
		items, _ := node["items"].(map[string]any)
		itemType, _ := items["type"].(string)
		if typ != "array" || itemType != "string" {
			return fmt.Errorf("split 只能用于 string array 字段")
		}
		if rule.Separator == "" || len(rule.Separator) > 32 {
			return fmt.Errorf("split separator 必须为 1..32 字节")
		}
	case "concat":
		if err := rejectUnusedNormalizationParams(rule, "source_fields", "separator"); err != nil {
			return err
		}
		if typ != "string" || len(rule.SourceFields) == 0 || len(rule.SourceFields) > 16 {
			return fmt.Errorf("concat 只能用于 string 字段且需要 1..16 个 source_fields")
		}
		seenSource := map[string]bool{}
		for j, source := range rule.SourceFields {
			source = strings.TrimSpace(source)
			if _, ok := properties[source]; !ok || seenSource[source] {
				return fmt.Errorf("concat source_field 不存在或重复: %s", source)
			}
			seenSource[source], rule.SourceFields[j] = true, source
		}
		if len(rule.Separator) > 64 {
			return fmt.Errorf("concat separator 不能超过 64 字节")
		}
	default:
		return fmt.Errorf("不支持 normalization operation %q", rule.Operation)
	}
	return nil
}

func normalizeValidationRules(source []domain.ValidationRule, properties map[string]any) ([]domain.ValidationRule, error) {
	if len(source) == 0 {
		return []domain.ValidationRule{}, nil
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxCleaningRulesJSONBytes {
		return nil, fmt.Errorf("validation_rules 超过大小限制")
	}
	rules := cloneValidationRules(source)
	if len(rules) > MaxCleaningRules {
		return nil, fmt.Errorf("validation_rules 最多 %d 条", MaxCleaningRules)
	}
	for i := range rules {
		rule := &rules[i]
		rule.Field = strings.TrimSpace(rule.Field)
		rule.Operation = domain.ValidationOperation(strings.TrimSpace(string(rule.Operation)))
		rule.Severity = domain.IssueSeverity(strings.TrimSpace(string(rule.Severity)))
		rule.Message = strings.TrimSpace(rule.Message)
		if rule.Severity == "" {
			rule.Severity = domain.SeverityError
		}
		if rule.Severity != domain.SeverityError && rule.Severity != domain.SeverityWarning {
			return nil, fmt.Errorf("validation_rules 第 %d 条 severity 只能是 error/warning", i+1)
		}
		if len(rule.Message) > MaxCleaningRuleMessage {
			return nil, fmt.Errorf("validation_rules 第 %d 条 message 过长", i+1)
		}
		node, ok := properties[rule.Field].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("validation_rules 第 %d 条引用了不存在的字段 %s", i+1, rule.Field)
		}
		if err := validateValidationRule(rule, node, properties); err != nil {
			return nil, fmt.Errorf("validation_rules 第 %d 条: %w", i+1, err)
		}
	}
	return rules, nil
}

func cloneValidationRules(source []domain.ValidationRule) []domain.ValidationRule {
	rules := append([]domain.ValidationRule(nil), source...)
	for index := range rules {
		rules[index].Values = append([]any(nil), rules[index].Values...)
	}
	return rules
}

func validateValidationRule(rule *domain.ValidationRule, node map[string]any, properties map[string]any) error {
	typ, _ := node["type"].(string)
	switch rule.Operation {
	case "required":
		if err := rejectUnusedValidationParams(rule); err != nil {
			return err
		}
	case "regex":
		if err := rejectUnusedValidationParams(rule, "pattern"); err != nil {
			return err
		}
		if typ != "string" || rule.Pattern == "" || len(rule.Pattern) > MaxSchemaPatternLength {
			return fmt.Errorf("regex 只能用于 string 字段且 pattern 必须非空")
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return fmt.Errorf("regex pattern 非法: %w", err)
		}
	case "range":
		if err := rejectUnusedValidationParams(rule, "minimum", "maximum"); err != nil {
			return err
		}
		if typ != "integer" && typ != "number" {
			return fmt.Errorf("range 只能用于 integer/number 字段")
		}
		if rule.Minimum == nil && rule.Maximum == nil {
			return fmt.Errorf("range 至少需要 minimum 或 maximum")
		}
		if rule.Minimum != nil && rule.Maximum != nil && *rule.Minimum > *rule.Maximum {
			return fmt.Errorf("range minimum 不能大于 maximum")
		}
	case "length":
		if err := rejectUnusedValidationParams(rule, "min_length", "max_length"); err != nil {
			return err
		}
		if typ != "string" && typ != "array" {
			return fmt.Errorf("length 只能用于 string/array 字段")
		}
		if rule.MinLength == nil && rule.MaxLength == nil {
			return fmt.Errorf("length 至少需要 min_length 或 max_length")
		}
		if rule.MinLength != nil && *rule.MinLength < 0 || rule.MaxLength != nil && *rule.MaxLength < 0 {
			return fmt.Errorf("length 边界不能为负数")
		}
		if rule.MinLength != nil && rule.MaxLength != nil && *rule.MinLength > *rule.MaxLength {
			return fmt.Errorf("min_length 不能大于 max_length")
		}
	case "one_of":
		if err := rejectUnusedValidationParams(rule, "values"); err != nil {
			return err
		}
		if len(rule.Values) == 0 || len(rule.Values) > MaxCleaningRuleValues {
			return fmt.Errorf("one_of values 必须包含 1..%d 项", MaxCleaningRuleValues)
		}
		for i, value := range rule.Values {
			rule.Values[i] = normalizeLooseValue(value)
			if err := validateJSONValue(node, rule.Values[i], "$."+rule.Field); err != nil {
				return fmt.Errorf("one_of 值不符合字段 Schema: %w", err)
			}
		}
	case "compare":
		if err := rejectUnusedValidationParams(rule, "other_field", "operator"); err != nil {
			return err
		}
		rule.OtherField = strings.TrimSpace(rule.OtherField)
		other, ok := properties[rule.OtherField].(map[string]any)
		if !ok || rule.OtherField == rule.Field {
			return fmt.Errorf("compare other_field 不存在或与 field 相同")
		}
		otherType, _ := other["type"].(string)
		if typ != otherType || (typ != "string" && typ != "integer" && typ != "number") {
			return fmt.Errorf("compare 两个字段必须是同类型 string/integer/number")
		}
		switch rule.Operator {
		case "eq", "ne", "lt", "lte", "gt", "gte":
		default:
			return fmt.Errorf("compare operator 非法")
		}
	default:
		return fmt.Errorf("不支持 validation operation %q", rule.Operation)
	}
	return nil
}

func rejectUnusedNormalizationParams(rule *domain.NormalizationRule, allowed ...string) error {
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allow[name] = true
	}
	used := map[string]bool{
		"aliases": len(rule.Aliases) > 0, "true_values": len(rule.TrueValues) > 0,
		"false_values": len(rule.FalseValues) > 0, "layouts": len(rule.Layouts) > 0,
		"units": len(rule.Units) > 0, "separator": rule.Separator != "",
		"source_fields": len(rule.SourceFields) > 0,
	}
	for name, present := range used {
		if present && !allow[name] {
			return fmt.Errorf("operation %s 不接受参数 %s", rule.Operation, name)
		}
	}
	return nil
}

func rejectUnusedValidationParams(rule *domain.ValidationRule, allowed ...string) error {
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allow[name] = true
	}
	used := map[string]bool{
		"pattern": rule.Pattern != "", "minimum": rule.Minimum != nil, "maximum": rule.Maximum != nil,
		"min_length": rule.MinLength != nil, "max_length": rule.MaxLength != nil,
		"values": len(rule.Values) > 0, "other_field": rule.OtherField != "", "operator": rule.Operator != "",
	}
	for name, present := range used {
		if present && !allow[name] {
			return fmt.Errorf("operation %s 不接受参数 %s", rule.Operation, name)
		}
	}
	return nil
}

// TransformRecord 对一条 LLM 草稿执行确定性转换。字段转换失败会作为 issue 返回，
// 原值仍被保留，供 data.validate 和人工审核处理；只有合同或 JSON 损坏才返回 error。
func TransformRecord(schemaPayload json.RawMessage, sourceRules []domain.NormalizationRule,
	fieldsRaw json.RawMessage) (json.RawMessage, []model.RecordChange, []model.RecordIssue, error) {
	properties, err := rootSchemaProperties(schemaPayload)
	if err != nil {
		return nil, nil, nil, err
	}
	rules, err := normalizeNormalizationRules(sourceRules, properties)
	if err != nil {
		return nil, nil, nil, err
	}
	var fields map[string]any
	if err := decodeSingleJSON(fieldsRaw, &fields); err != nil || fields == nil {
		return nil, nil, nil, fmt.Errorf("RecordDraft fields 必须是 JSON object")
	}
	original := cloneJSONObject(fields)
	fields = normalizeLooseValue(fields).(map[string]any)
	ruleByField := make(map[string]domain.NormalizationRule, len(rules))
	for _, rule := range rules {
		ruleByField[rule.Field] = rule
	}

	issues := make([]model.RecordIssue, 0)
	for _, field := range sortedAnyMapKeys(properties) {
		rawNode := properties[field]
		node := rawNode.(map[string]any)
		value, exists := fields[field]
		rule, hasRule := ruleByField[field]
		if hasRule && rule.Operation == "concat" {
			continue
		}
		if !exists {
			continue
		}
		if hasRule {
			var ruleIssue *model.RecordIssue
			value, ruleIssue = applyNormalizationRule(rule, node, value)
			if ruleIssue != nil {
				issues = append(issues, *ruleIssue)
				continue
			}
		}
		coerced, conversionIssues := coerceValueForSchema(node, value, field)
		fields[field] = coerced
		issues = append(issues, conversionIssues...)
	}
	for _, rule := range rules {
		if rule.Operation != "concat" {
			continue
		}
		parts := make([]string, 0, len(rule.SourceFields))
		for _, source := range rule.SourceFields {
			if value, ok := fields[source]; ok && value != nil {
				text := strings.TrimSpace(fmt.Sprint(value))
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		fields[rule.Field] = strings.Join(parts, rule.Separator)
	}

	changes := diffTopLevelFields(original, fields, ruleByField)
	canonical, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("序列化转换结果: %w", err)
	}
	return canonical, changes, issues, nil
}

// ValidateTransformedRecord 执行 JSON Schema 和声明式业务规则校验。Schema 校验
// 失败时仍返回原字段，确保无效记录可以进入审核区而不是从管线中消失。
func ValidateTransformedRecord(schemaPayload json.RawMessage, sourceRules []domain.ValidationRule,
	fieldsRaw json.RawMessage) (json.RawMessage, []model.RecordIssue, error) {
	properties, err := rootSchemaProperties(schemaPayload)
	if err != nil {
		return nil, nil, err
	}
	rules, err := normalizeValidationRules(sourceRules, properties)
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]any
	if err := decodeSingleJSON(fieldsRaw, &fields); err != nil || fields == nil {
		return nil, nil, fmt.Errorf("TransformedRecord fields 必须是 JSON object")
	}
	canonical, schemaErr := NormalizeDatasetItem(schemaPayload, fieldsRaw)
	issues := make([]model.RecordIssue, 0)
	if schemaErr != nil {
		canonical, _ = json.Marshal(fields)
		issues = append(issues, model.RecordIssue{Code: "schema_invalid", Field: fieldFromValidationMessage(schemaErr.Error()),
			Severity: model.RecordIssueError, Message: schemaErr.Error()})
	}
	for _, rule := range rules {
		if issue := evaluateValidationRule(rule, fields); issue != nil {
			issues = append(issues, *issue)
		}
	}
	return canonical, issues, nil
}

func applyNormalizationRule(rule domain.NormalizationRule, node map[string]any, value any) (any, *model.RecordIssue) {
	fail := func(code, message string) (any, *model.RecordIssue) {
		return value, &model.RecordIssue{Code: code, Field: rule.Field, Severity: model.RecordIssueError, Message: message}
	}
	switch rule.Operation {
	case "enum_alias":
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		if target, exists := rule.Aliases[normalizeText(text)]; exists {
			return normalizeLooseValue(target), nil
		}
	case "boolean_alias":
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		key := strings.ToLower(normalizeText(text))
		if containsNormalized(rule.TrueValues, key) {
			return true, nil
		}
		if containsNormalized(rule.FalseValues, key) {
			return false, nil
		}
		return fail("boolean_alias_unknown", fmt.Sprintf("字段 %s 的值 %q 不在布尔别名表中", rule.Field, text))
	case "date":
		text, ok := value.(string)
		if !ok {
			return fail("date_type_invalid", fmt.Sprintf("字段 %s 不是可解析的日期字符串", rule.Field))
		}
		for _, layout := range rule.Layouts {
			if parsed, err := time.Parse(layout, text); err == nil {
				if format, _ := node["format"].(string); format == "date" {
					return parsed.Format("2006-01-02"), nil
				}
				return parsed.Format(time.RFC3339), nil
			}
		}
		return fail("date_parse_failed", fmt.Sprintf("字段 %s 无法按声明格式解析日期", rule.Field))
	case "unit_scale":
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		match := unitValuePattern.FindStringSubmatch(normalizeText(text))
		if len(match) != 3 {
			return fail("unit_parse_failed", fmt.Sprintf("字段 %s 不是“数值+单位”格式", rule.Field))
		}
		number, err := strconv.ParseFloat(match[1], 64)
		factor, found := rule.Units[strings.ToLower(match[2])]
		if err != nil || !found {
			return fail("unit_unknown", fmt.Sprintf("字段 %s 的单位 %s 未声明", rule.Field, match[2]))
		}
		return json.Number(strconv.FormatFloat(number*factor, 'f', -1, 64)), nil
	case "split":
		text, ok := value.(string)
		if !ok {
			return value, nil
		}
		parts := strings.Split(text, rule.Separator)
		values := make([]any, 0, len(parts))
		for _, part := range parts {
			if part = normalizeText(part); part != "" {
				values = append(values, part)
			}
		}
		return values, nil
	}
	return value, nil
}

func coerceValueForSchema(schema map[string]any, value any, path string) (any, []model.RecordIssue) {
	typ, _ := schema["type"].(string)
	issue := func(message string) (any, []model.RecordIssue) {
		return value, []model.RecordIssue{{Code: "type_conversion_failed", Field: path,
			Severity: model.RecordIssueError, Message: message}}
	}
	switch typ {
	case "string":
		switch typed := value.(type) {
		case string:
			return normalizeText(typed), nil
		case json.Number, bool:
			return fmt.Sprint(typed), nil
		default:
			return issue(fmt.Sprintf("字段 %s 无法确定性转换为 string", path))
		}
	case "integer":
		switch typed := value.(type) {
		case json.Number:
			if _, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
				return typed, nil
			}
		case string:
			if parsed, err := strconv.ParseInt(normalizeNumericText(typed), 10, 64); err == nil {
				return json.Number(strconv.FormatInt(parsed, 10)), nil
			}
		}
		return issue(fmt.Sprintf("字段 %s 无法确定性转换为 integer", path))
	case "number":
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := strconv.ParseFloat(string(typed), 64); err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) {
				return json.Number(strconv.FormatFloat(parsed, 'f', -1, 64)), nil
			}
		case string:
			if parsed, err := strconv.ParseFloat(normalizeNumericText(typed), 64); err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) {
				return json.Number(strconv.FormatFloat(parsed, 'f', -1, 64)), nil
			}
		}
		return issue(fmt.Sprintf("字段 %s 无法确定性转换为 number", path))
	case "boolean":
		if typed, ok := value.(bool); ok {
			return typed, nil
		}
		if typed, ok := value.(string); ok {
			switch strings.ToLower(normalizeText(typed)) {
			case "true", "yes", "y", "1", "是", "真", "开启", "启用":
				return true, nil
			case "false", "no", "n", "0", "否", "假", "关闭", "停用":
				return false, nil
			}
		}
		return issue(fmt.Sprintf("字段 %s 无法确定性转换为 boolean", path))
	case "array":
		array, ok := value.([]any)
		if !ok {
			return issue(fmt.Sprintf("字段 %s 无法确定性转换为 array", path))
		}
		itemSchema, _ := schema["items"].(map[string]any)
		issues := make([]model.RecordIssue, 0)
		for i := range array {
			coerced, childIssues := coerceValueForSchema(itemSchema, array[i], fmt.Sprintf("%s[%d]", path, i))
			array[i] = coerced
			issues = append(issues, childIssues...)
		}
		return array, issues
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return issue(fmt.Sprintf("字段 %s 无法确定性转换为 object", path))
		}
		properties, _ := schema["properties"].(map[string]any)
		issues := make([]model.RecordIssue, 0)
		for _, field := range sortedAnyMapKeys(object) {
			child := object[field]
			childSchema, exists := properties[field].(map[string]any)
			if !exists {
				continue
			}
			coerced, childIssues := coerceValueForSchema(childSchema, child, path+"."+field)
			object[field] = coerced
			issues = append(issues, childIssues...)
		}
		return object, issues
	default:
		return issue(fmt.Sprintf("字段 %s 的 Schema type 非法", path))
	}
}

func evaluateValidationRule(rule domain.ValidationRule, fields map[string]any) *model.RecordIssue {
	value, exists := fields[rule.Field]
	message := rule.Message
	issue := func(code, fallback string) *model.RecordIssue {
		if message == "" {
			message = fallback
		}
		return &model.RecordIssue{Code: code, Field: rule.Field, Severity: string(rule.Severity), Message: message}
	}
	if rule.Operation == "required" {
		if !exists || isEmptyRecordValue(value) {
			return issue("rule_required", fmt.Sprintf("字段 %s 不能为空", rule.Field))
		}
		return nil
	}
	if !exists || value == nil {
		return nil
	}
	switch rule.Operation {
	case "regex":
		text, ok := value.(string)
		if ok && !regexp.MustCompile(rule.Pattern).MatchString(text) {
			return issue("rule_regex", fmt.Sprintf("字段 %s 不符合业务格式", rule.Field))
		}
	case "range":
		number, ok := recordNumber(value)
		if ok && (rule.Minimum != nil && number < *rule.Minimum || rule.Maximum != nil && number > *rule.Maximum) {
			return issue("rule_range", fmt.Sprintf("字段 %s 超出业务范围", rule.Field))
		}
	case "length":
		length := -1
		switch typed := value.(type) {
		case string:
			length = len([]rune(typed))
		case []any:
			length = len(typed)
		}
		if length >= 0 && (rule.MinLength != nil && length < *rule.MinLength || rule.MaxLength != nil && length > *rule.MaxLength) {
			return issue("rule_length", fmt.Sprintf("字段 %s 长度不符合业务规则", rule.Field))
		}
	case "one_of":
		actual, _ := json.Marshal(value)
		matched := false
		for _, candidate := range rule.Values {
			raw, _ := json.Marshal(normalizeLooseValue(candidate))
			if bytes.Equal(actual, raw) {
				matched = true
				break
			}
		}
		if !matched {
			return issue("rule_one_of", fmt.Sprintf("字段 %s 不在业务允许值中", rule.Field))
		}
	case "compare":
		other, ok := fields[rule.OtherField]
		if !ok || other == nil {
			return nil
		}
		comparison, comparable := compareRecordValues(value, other)
		passed := comparable && map[string]bool{"eq": comparison == 0, "ne": comparison != 0,
			"lt": comparison < 0, "lte": comparison <= 0, "gt": comparison > 0, "gte": comparison >= 0}[rule.Operator]
		if !passed {
			return issue("rule_compare", fmt.Sprintf("字段 %s 与 %s 的关系不符合 %s", rule.Field, rule.OtherField, rule.Operator))
		}
	}
	return nil
}

func rootSchemaProperties(payload json.RawMessage) (map[string]any, error) {
	var root map[string]any
	if err := decodeSingleJSON(payload, &root); err != nil {
		return nil, fmt.Errorf("目标 JSON Schema 非法: %w", err)
	}
	properties, ok := root["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return nil, fmt.Errorf("目标 JSON Schema 缺少 properties")
	}
	return properties, nil
}

func normalizeLooseValue(value any) any {
	switch typed := value.(type) {
	case string:
		return normalizeText(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = normalizeLooseValue(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = normalizeLooseValue(typed[i])
		}
		return out
	default:
		return value
	}
}

func normalizeText(value string) string {
	// 领域层保持零三方依赖。首期受控兼容归一化覆盖最常见的全角 ASCII 与全角空格；
	// 规则行为由 TransformEngineVersion 固化，后续扩展字符表时必须升级引擎版本。
	compatible := strings.Map(func(r rune) rune {
		switch {
		case r == '\u3000':
			return ' '
		case r >= '\uFF01' && r <= '\uFF5E':
			return r - 0xFEE0
		default:
			return r
		}
	}, value)
	return strings.TrimFunc(compatible, unicode.IsSpace)
}

func normalizeNumericText(value string) string {
	return strings.ReplaceAll(normalizeText(value), ",", "")
}

func normalizeStringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.ToLower(normalizeText(value)); value != "" {
			out[value] = true
		}
	}
	return out
}

func sortedSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsNormalized(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneJSONObject(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var cloned map[string]any
	_ = decodeSingleJSON(raw, &cloned)
	return cloned
}

func sortedAnyMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func diffTopLevelFields(before, after map[string]any, rules map[string]domain.NormalizationRule) []model.RecordChange {
	keys := make(map[string]bool, len(before)+len(after))
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	names := make([]string, 0, len(keys))
	for key := range keys {
		names = append(names, key)
	}
	sort.Strings(names)
	changes := make([]model.RecordChange, 0)
	for _, field := range names {
		oldRaw, _ := json.Marshal(before[field])
		newRaw, _ := json.Marshal(after[field])
		if bytes.Equal(oldRaw, newRaw) {
			continue
		}
		operation := "schema_coerce"
		if rule, ok := rules[field]; ok {
			operation = string(rule.Operation)
		}
		changes = append(changes, model.RecordChange{Field: field, Operation: operation,
			Before: oldRaw, After: newRaw})
	}
	return changes
}

func fieldFromValidationMessage(message string) string {
	const marker = "字段 $."
	start := strings.Index(message, marker)
	if start < 0 {
		return ""
	}
	rest := message[start+len(marker):]
	end := strings.IndexAny(rest, " [:")
	if end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

func isEmptyRecordValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func recordNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		n, err := strconv.ParseFloat(string(typed), 64)
		return n, err == nil
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	default:
		return 0, false
	}
}

func compareRecordValues(left, right any) (int, bool) {
	if a, ok := recordNumber(left); ok {
		b, ok := recordNumber(right)
		if !ok {
			return 0, false
		}
		switch {
		case a < b:
			return -1, true
		case a > b:
			return 1, true
		default:
			return 0, true
		}
	}
	a, aOK := left.(string)
	b, bOK := right.(string)
	if !aOK || !bOK {
		return 0, false
	}
	return strings.Compare(a, b), true
}
