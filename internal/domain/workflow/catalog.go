package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type CapabilityCatalog interface {
	Lookup(ref CapabilityRef) (CapabilityDefinition, bool)
}

type StaticCatalog struct {
	definitions map[string]CapabilityDefinition
}

func NewStaticCatalog(definitions ...CapabilityDefinition) (*StaticCatalog, error) {
	catalog := &StaticCatalog{definitions: make(map[string]CapabilityDefinition, len(definitions))}
	for _, definition := range definitions {
		if err := validateCapability(definition); err != nil {
			return nil, err
		}
		key := capabilityKey(definition.Ref)
		if _, exists := catalog.definitions[key]; exists {
			return nil, fmt.Errorf("capability %s 重复注册", key)
		}
		catalog.definitions[key] = cloneCapability(definition)
	}
	return catalog, nil
}

func (c *StaticCatalog) Lookup(ref CapabilityRef) (CapabilityDefinition, bool) {
	if c == nil {
		return CapabilityDefinition{}, false
	}
	definition, ok := c.definitions[capabilityKey(ref)]
	return cloneCapability(definition), ok
}

func (c *StaticCatalog) Definitions() []CapabilityDefinition {
	if c == nil {
		return nil
	}
	result := make([]CapabilityDefinition, 0, len(c.definitions))
	for _, definition := range c.definitions {
		result = append(result, cloneCapability(definition))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Ref.Kind == result[j].Ref.Kind {
			return result[i].Ref.Version < result[j].Ref.Version
		}
		return result[i].Ref.Kind < result[j].Ref.Kind
	})
	return result
}

func capabilityKey(ref CapabilityRef) string {
	return fmt.Sprintf("%s@%d", ref.Kind, ref.Version)
}

func validateCapability(definition CapabilityDefinition) error {
	if !validCapabilityKind(definition.Ref.Kind) || definition.Ref.Version < 1 {
		return fmt.Errorf("capability 引用非法: %s", capabilityKey(definition.Ref))
	}
	if strings.TrimSpace(definition.Label) == "" || strings.TrimSpace(definition.Description) == "" {
		return fmt.Errorf("capability %s 缺少名称或说明", capabilityKey(definition.Ref))
	}
	if definition.RequiresLLM && !definition.ManualCompletion {
		return fmt.Errorf("capability %s 依赖 LLM，但未声明人工完成能力", capabilityKey(definition.Ref))
	}
	if err := validateConfigSchema(definition.ConfigSchema); err != nil {
		return fmt.Errorf("capability %s: %w", capabilityKey(definition.Ref), err)
	}
	if err := validateJSONObject("default_config", definition.DefaultConfig); err != nil {
		return fmt.Errorf("capability %s: %w", capabilityKey(definition.Ref), err)
	}
	if err := ValidateCapabilityConfig(definition, definition.DefaultConfig); err != nil {
		return fmt.Errorf("capability %s 的 default_config: %w", capabilityKey(definition.Ref), err)
	}
	requirements := map[RuleSection]bool{}
	for _, requirement := range definition.RuleRequirements {
		if requirement != RuleDataContract && requirement != RuleExtraction && requirement != RuleSearch && requirement != RuleOutputContract {
			return fmt.Errorf("capability %s 的规则要求 %q 非法", capabilityKey(definition.Ref), requirement)
		}
		if requirements[requirement] {
			return fmt.Errorf("capability %s 的规则要求 %q 重复", capabilityKey(definition.Ref), requirement)
		}
		requirements[requirement] = true
	}
	inputNames := map[string]bool{}
	outputNames := map[string]bool{}
	primaryInputs, primaryOutputs := 0, 0
	for _, port := range definition.Inputs {
		if err := validatePort(port); err != nil {
			return fmt.Errorf("capability %s 输入端口: %w", capabilityKey(definition.Ref), err)
		}
		if inputNames[port.Name] {
			return fmt.Errorf("capability %s 输入端口 %s 重复", capabilityKey(definition.Ref), port.Name)
		}
		inputNames[port.Name] = true
		if port.Role == PortPrimary {
			primaryInputs++
			if !port.Required || port.Multiple {
				return fmt.Errorf("capability %s 的主输入必须为必填单值端口", capabilityKey(definition.Ref))
			}
		}
	}
	for _, port := range definition.Outputs {
		if err := validatePort(port); err != nil {
			return fmt.Errorf("capability %s 输出端口: %w", capabilityKey(definition.Ref), err)
		}
		if outputNames[port.Name] {
			return fmt.Errorf("capability %s 输出端口 %s 重复", capabilityKey(definition.Ref), port.Name)
		}
		outputNames[port.Name] = true
		if port.Role == PortSide {
			return fmt.Errorf("capability %s 的输出端口不能使用 side 角色", capabilityKey(definition.Ref))
		}
		if port.Role == PortPrimary {
			primaryOutputs++
			if port.Multiple {
				return fmt.Errorf("capability %s 的主输出必须为单值端口", capabilityKey(definition.Ref))
			}
		}
	}
	if primaryInputs != 1 || primaryOutputs != 1 {
		return fmt.Errorf("capability %s 必须且只能声明一个主输入和一个主输出", capabilityKey(definition.Ref))
	}
	return nil
}

func validatePort(port PortDefinition) error {
	if !validIdentifier(port.Name) {
		return fmt.Errorf("端口名 %q 非法", port.Name)
	}
	if strings.TrimSpace(port.Label) == "" || strings.TrimSpace(string(port.ResourceType)) == "" {
		return fmt.Errorf("端口 %s 缺少名称或资源类型", port.Name)
	}
	if port.Role != PortPrimary && port.Role != PortSide && port.Role != PortDelivery {
		return fmt.Errorf("端口 %s 的角色 %q 非法", port.Name, port.Role)
	}
	return nil
}

func validateJSONDocument(label string, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("%s 不是合法 JSON", label)
	}
	return nil
}

func validateJSONObject(label string, raw json.RawMessage) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return fmt.Errorf("%s 必须是 JSON object", label)
	}
	return nil
}

func cloneCapability(definition CapabilityDefinition) CapabilityDefinition {
	definition.Inputs = append([]PortDefinition(nil), definition.Inputs...)
	definition.Outputs = append([]PortDefinition(nil), definition.Outputs...)
	definition.RuleRequirements = append([]RuleSection(nil), definition.RuleRequirements...)
	definition.ConfigSchema = append(json.RawMessage(nil), definition.ConfigSchema...)
	definition.DefaultConfig = append(json.RawMessage(nil), definition.DefaultConfig...)
	return definition
}
