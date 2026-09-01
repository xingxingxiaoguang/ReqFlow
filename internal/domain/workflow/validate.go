package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type ValidationMode string

const (
	ValidateDraft   ValidationMode = "draft"
	ValidatePublish ValidationMode = "publish"
)

type IssueSeverity string

const (
	SeverityWarning IssueSeverity = "warning"
	SeverityError   IssueSeverity = "error"
)

type ValidationIssue struct {
	Code     string        `json:"code"`
	Path     string        `json:"path"`
	Message  string        `json:"message"`
	Severity IssueSeverity `json:"severity"`
}

var (
	identifierPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	capabilityKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,126}$`)
)

func validIdentifier(value string) bool     { return identifierPattern.MatchString(value) }
func validCapabilityKind(value string) bool { return capabilityKindPattern.MatchString(value) }

func HasErrors(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			return true
		}
	}
	return false
}

func Validate(draft WorkflowDraft, catalog CapabilityCatalog, mode ValidationMode) []ValidationIssue {
	validator := draftValidator{draft: draft, catalog: catalog, mode: mode}
	validator.validateIdentity()
	validator.indexDefinition()
	validator.validateConnections()
	validator.validateTopology()
	validator.validateRules()
	validator.validateAcceptanceCases()
	sort.SliceStable(validator.issues, func(i, j int) bool {
		if validator.issues[i].Severity != validator.issues[j].Severity {
			return validator.issues[i].Severity == SeverityError
		}
		if validator.issues[i].Path == validator.issues[j].Path {
			return validator.issues[i].Code < validator.issues[j].Code
		}
		return validator.issues[i].Path < validator.issues[j].Path
	})
	return validator.issues
}

type draftValidator struct {
	draft        WorkflowDraft
	catalog      CapabilityCatalog
	mode         ValidationMode
	issues       []ValidationIssue
	nodes        map[string]WorkflowNode
	capabilities map[string]CapabilityDefinition
	inputs       map[string]WorkflowPort
	outputs      map[string]WorkflowPort
	incoming     map[string]int
	outgoing     map[string]int
	nodeEdges    map[string]string
	nodeParents  map[string]string
}

func (v *draftValidator) validateIdentity() {
	if v.mode != ValidateDraft && v.mode != ValidatePublish {
		v.add("validation_mode_invalid", "", "未知校验模式", SeverityError)
	}
	if v.mode == ValidatePublish {
		if !validIdentifier(v.draft.Key) {
			v.add("workflow_key_invalid", "key", "流程编码必须是小写 snake_case", SeverityError)
		}
		if strings.TrimSpace(v.draft.Name) == "" {
			v.add("workflow_name_required", "name", "流程名称不能为空", SeverityError)
		}
		if strings.TrimSpace(v.draft.WorkspaceID) == "" {
			v.add("workspace_required", "workspace_id", "workspace 不能为空", SeverityError)
		}
	}
	if v.draft.Revision < 0 {
		v.add("draft_revision_invalid", "revision", "草稿版本不能为负数", SeverityError)
	}
}

func (v *draftValidator) indexDefinition() {
	v.nodes = make(map[string]WorkflowNode, len(v.draft.Nodes))
	v.capabilities = make(map[string]CapabilityDefinition, len(v.draft.Nodes))
	v.inputs = make(map[string]WorkflowPort, len(v.draft.Inputs))
	v.outputs = make(map[string]WorkflowPort, len(v.draft.Outputs))
	v.incoming = map[string]int{}
	v.outgoing = map[string]int{}
	v.nodeEdges = map[string]string{}
	v.nodeParents = map[string]string{}

	for i, input := range v.draft.Inputs {
		path := fmt.Sprintf("inputs[%d]", i)
		if err := validateWorkflowPort(input); err != nil {
			v.add("workflow_input_invalid", path, err.Error(), SeverityError)
		}
		if _, exists := v.inputs[input.Name]; exists {
			v.add("workflow_input_duplicate", path+".name", "流程输入端口重复", SeverityError)
		}
		v.inputs[input.Name] = input
	}
	for i, output := range v.draft.Outputs {
		path := fmt.Sprintf("outputs[%d]", i)
		if err := validateWorkflowPort(output); err != nil {
			v.add("workflow_output_invalid", path, err.Error(), SeverityError)
		}
		if _, exists := v.outputs[output.Name]; exists {
			v.add("workflow_output_duplicate", path+".name", "流程输出端口重复", SeverityError)
		}
		v.outputs[output.Name] = output
	}
	for i, node := range v.draft.Nodes {
		path := fmt.Sprintf("nodes[%d]", i)
		if !validIdentifier(node.ID) {
			v.add("node_id_invalid", path+".id", "节点 ID 必须是小写 snake_case", SeverityError)
		}
		if strings.TrimSpace(node.Name) == "" {
			v.add("node_name_required", path+".name", "节点名称不能为空", v.requiredSeverity())
		}
		if _, exists := v.nodes[node.ID]; exists {
			v.add("node_id_duplicate", path+".id", "节点 ID 重复", SeverityError)
		}
		v.nodes[node.ID] = node
		if v.catalog == nil {
			v.add("capability_catalog_missing", path+".capability", "Capability Catalog 未配置", SeverityError)
			continue
		}
		capability, exists := v.catalog.Lookup(node.Capability)
		if !exists {
			v.add("capability_not_found", path+".capability", "节点引用了未注册的 Capability", SeverityError)
			continue
		}
		v.capabilities[node.ID] = capability
		if err := validateJSONObject("节点配置", node.Config); err != nil {
			v.add("node_config_invalid", path+".config", err.Error(), SeverityError)
		}
	}
	if len(v.draft.Nodes) == 0 && v.mode == ValidatePublish {
		v.add("workflow_nodes_required", "nodes", "发布流程至少需要一个节点", SeverityError)
	}
}

func (v *draftValidator) validateConnections() {
	seenConnections := map[string]bool{}
	for i, connection := range v.draft.Connections {
		path := fmt.Sprintf("connections[%d]", i)
		key := endpointKey(connection.From) + "->" + endpointKey(connection.To)
		if seenConnections[key] {
			v.add("connection_duplicate", path, "连接重复", SeverityError)
			continue
		}
		seenConnections[key] = true

		source, sourceOK := v.resolveSource(connection.From, path+".from")
		target, targetOK := v.resolveTarget(connection.To, path+".to")
		if !sourceOK || !targetOK {
			continue
		}
		if source.resourceType != target.resourceType {
			v.add("connection_type_mismatch", path,
				fmt.Sprintf("连接资源类型不匹配：%s → %s", source.resourceType, target.resourceType), SeverityError)
		}
		v.outgoing[endpointKey(connection.From)]++
		targetKey := endpointKey(connection.To)
		v.incoming[targetKey]++
		if v.incoming[targetKey] > 1 && !target.multiple {
			v.add("connection_target_occupied", path+".to", "单值输入端口只能连接一个来源", SeverityError)
		}

		if connection.From.Kind == EndpointNodeOutput && connection.To.Kind == EndpointNodeInput {
			if source.role != PortPrimary || target.role != PortPrimary {
				v.add("linear_side_connection_forbidden", path,
					"线性流程的节点间连接必须由主输出连接到主输入；侧输入只能来自流程输入", SeverityError)
				continue
			}
			from, to := connection.From.NodeID, connection.To.NodeID
			if from == to {
				v.add("linear_self_connection", path, "节点不能连接自身", SeverityError)
				continue
			}
			if existing, ok := v.nodeEdges[from]; ok && existing != to {
				v.add("linear_branch_forbidden", path, "线性流程不允许一个节点连接多个后继", SeverityError)
			} else {
				v.nodeEdges[from] = to
			}
			if existing, ok := v.nodeParents[to]; ok && existing != from {
				v.add("linear_merge_forbidden", path, "线性流程不允许一个节点连接多个前驱", SeverityError)
			} else {
				v.nodeParents[to] = from
			}
		}
	}

	for _, node := range v.draft.Nodes {
		capability, ok := v.capabilities[node.ID]
		if !ok {
			continue
		}
		for _, port := range capability.Inputs {
			count := v.incoming[endpointKey(Endpoint{Kind: EndpointNodeInput, NodeID: node.ID, Port: port.Name})]
			if port.Required && count == 0 {
				v.add("required_input_unconnected", "nodes."+node.ID+".inputs."+port.Name,
					fmt.Sprintf("节点 %s 的必填输入 %s 尚未连接", node.Name, port.Label), v.requiredSeverity())
			}
		}
	}
	for _, input := range v.draft.Inputs {
		count := v.outgoing[endpointKey(Endpoint{Kind: EndpointWorkflowInput, Port: input.Name})]
		if input.Required && count == 0 {
			v.add("workflow_input_unused", "inputs."+input.Name,
				fmt.Sprintf("必填流程输入 %s 尚未被任何节点使用", input.Label), v.requiredSeverity())
		}
	}
	for _, output := range v.draft.Outputs {
		count := v.incoming[endpointKey(Endpoint{Kind: EndpointWorkflowOutput, Port: output.Name})]
		if count == 0 {
			v.add("workflow_output_unconnected", "outputs."+output.Name,
				fmt.Sprintf("流程输出 %s 尚未连接", output.Label), v.requiredSeverity())
		}
	}
}

func (v *draftValidator) validateTopology() {
	if len(v.draft.Nodes) == 0 {
		return
	}
	if len(v.nodeEdges) != len(v.draft.Nodes)-1 {
		v.add("linear_chain_incomplete", "connections",
			"节点主连接必须形成一条覆盖全部节点的完整单链", v.requiredSeverity())
	}
	heads := make([]string, 0, 1)
	for id := range v.nodes {
		if _, hasParent := v.nodeParents[id]; !hasParent {
			heads = append(heads, id)
		}
	}
	if len(heads) != 1 {
		v.add("linear_chain_head_invalid", "connections", "线性流程必须且只能有一个起始节点", v.requiredSeverity())
		return
	}
	visited := map[string]bool{}
	for current := heads[0]; current != ""; current = v.nodeEdges[current] {
		if visited[current] {
			v.add("linear_cycle_forbidden", "connections", "线性流程不允许出现循环", SeverityError)
			return
		}
		visited[current] = true
	}
	if len(visited) != len(v.nodes) {
		v.add("linear_chain_disconnected", "connections", "节点主连接没有覆盖全部节点", v.requiredSeverity())
	}
}

func (v *draftValidator) validateRules() {
	needed := map[RuleSection][]string{}
	for nodeID, capability := range v.capabilities {
		for _, requirement := range capability.RuleRequirements {
			needed[requirement] = append(needed[requirement], nodeID)
		}
	}
	for requirement, nodes := range needed {
		if !hasRuleSection(v.draft.Rules, requirement) {
			v.add("required_rule_section_missing", "rules."+string(requirement),
				fmt.Sprintf("节点 %s 需要 %s 规则", strings.Join(nodes, "、"), requirement), v.requiredSeverity())
		}
	}
	if contract := v.draft.Rules.DataContract; contract != nil {
		v.validateDataContract(*contract)
	}
	if extraction := v.draft.Rules.Extraction; extraction != nil {
		v.validateExtraction(*extraction)
	}
	if search := v.draft.Rules.Search; search != nil {
		v.validateSearch(*search)
	}
	if output := v.draft.Rules.OutputContract; output != nil {
		v.validateFields("rules.output_contract.fields", output.Fields)
	}
	v.validateDecisions()
}

func (v *draftValidator) validateDataContract(contract DataContract) {
	if strings.TrimSpace(contract.RecordGranularity) == "" {
		v.add("record_granularity_required", "rules.data_contract.record_granularity",
			"必须说明一条记录代表什么", v.requiredSeverity())
	}
	fields := v.validateFields("rules.data_contract.fields", contract.Fields)
	if len(contract.KeyFields) == 0 {
		v.add("key_fields_required", "rules.data_contract.key_fields", "必须确认业务唯一键", v.requiredSeverity())
	}
	seen := map[string]bool{}
	for i, key := range contract.KeyFields {
		path := fmt.Sprintf("rules.data_contract.key_fields[%d]", i)
		field, exists := fields[key]
		if !exists {
			v.add("key_field_not_found", path, "唯一键引用了不存在的字段", SeverityError)
		}
		if exists && !field.Required {
			v.add("key_field_must_be_required", path, "唯一键字段必须为必填字段", SeverityError)
		}
		if seen[key] {
			v.add("key_field_duplicate", path, "唯一键字段重复", SeverityError)
		}
		seen[key] = true
	}
}

func (v *draftValidator) validateFields(path string, fields []FieldContract) map[string]FieldContract {
	result := make(map[string]FieldContract, len(fields))
	if len(fields) == 0 {
		v.add("contract_fields_required", path, "合同至少需要一个字段", v.requiredSeverity())
		return result
	}
	for i, field := range fields {
		fieldPath := fmt.Sprintf("%s[%d]", path, i)
		if !validIdentifier(field.Key) {
			v.add("field_key_invalid", fieldPath+".key", "字段编码必须是小写 snake_case", SeverityError)
		}
		if strings.TrimSpace(field.Label) == "" {
			v.add("field_label_required", fieldPath+".label", "字段名称不能为空", v.requiredSeverity())
		}
		if !validFieldType(field.Type) {
			v.add("field_type_invalid", fieldPath+".type", "字段类型非法", SeverityError)
		}
		if _, exists := result[field.Key]; exists {
			v.add("field_key_duplicate", fieldPath+".key", "字段编码重复", SeverityError)
		}
		result[field.Key] = field
		if field.Type == FieldArray && field.Items == nil {
			v.add("array_items_required", fieldPath+".items", "数组字段必须声明元素合同", SeverityError)
		}
		if field.Type == FieldObject {
			v.validateFields(fieldPath+".properties", field.Properties)
		}
		if field.Type != FieldString && len(field.Enum) > 0 {
			v.add("enum_type_invalid", fieldPath+".enum", "首期枚举只支持 string 字段", SeverityError)
		}
	}
	return result
}

func (v *draftValidator) validateExtraction(spec ExtractionSpec) {
	if strings.TrimSpace(spec.Instruction) == "" {
		v.add("extraction_instruction_required", "rules.extraction.instruction",
			"抽取规则必须说明证据和缺失值处理原则", v.requiredSeverity())
	}
	fields := map[string]bool{}
	if v.draft.Rules.DataContract != nil {
		for _, field := range v.draft.Rules.DataContract.Fields {
			fields[field.Key] = true
		}
	}
	for key := range spec.FieldGuides {
		if !fields[key] {
			v.add("field_guide_unknown_field", "rules.extraction.field_guides."+key,
				"字段指南引用了不存在的数据字段", SeverityError)
		}
	}
	v.validateRuleExpressions("rules.extraction.normalization_rules", spec.NormalizationRules)
	v.validateRuleExpressions("rules.extraction.validation_rules", spec.ValidationRules)
}

func (v *draftValidator) validateSearch(spec SearchSpec) {
	if spec.Preset != SearchBalanced && spec.Preset != SearchPrecise && spec.Preset != SearchSemantic && spec.Preset != SearchFilterFocused {
		v.add("search_preset_invalid", "rules.search.preset", "搜索策略预设非法", SeverityError)
	}
	fields := map[string]FieldContract{}
	if v.draft.Rules.DataContract != nil {
		for _, field := range v.draft.Rules.DataContract.Fields {
			fields[field.Key] = field
		}
	}
	seen := map[string]bool{}
	for i, weighted := range spec.LexicalFields {
		path := fmt.Sprintf("rules.search.lexical_fields[%d]", i)
		if _, exists := fields[weighted.Field]; !exists {
			v.add("search_field_not_found", path+".field", "精准搜索字段不存在", SeverityError)
		}
		if weighted.Weight <= 0 {
			v.add("search_weight_invalid", path+".weight", "搜索字段权重必须大于 0", SeverityError)
		}
		if seen[weighted.Field] {
			v.add("search_field_duplicate", path+".field", "精准搜索字段重复", SeverityError)
		}
		seen[weighted.Field] = true
	}
	v.validateSearchFieldList("rules.search.vector_fields", spec.VectorFields, fields, true)
	v.validateSearchFieldList("rules.search.filter_fields", spec.FilterFields, fields, false)
	if len(spec.LexicalFields) == 0 && len(spec.VectorFields) == 0 {
		v.add("search_fields_required", "rules.search", "至少配置一个精准或语义搜索字段", v.requiredSeverity())
	}
	if spec.ChunkSize <= 0 || spec.ChunkOverlap < 0 || spec.ChunkOverlap >= spec.ChunkSize {
		v.add("search_chunk_invalid", "rules.search", "分块长度必须大于 0，重叠必须小于分块长度", SeverityError)
	}
	if spec.LexicalCandidates <= 0 || spec.VectorCandidates <= 0 {
		v.add("search_candidates_invalid", "rules.search", "搜索候选数量必须大于 0", SeverityError)
	}
}

func (v *draftValidator) validateSearchFieldList(path string, values []string,
	fields map[string]FieldContract, textOnly bool) {
	seen := map[string]bool{}
	for i, value := range values {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		field, exists := fields[value]
		if !exists {
			v.add("search_field_not_found", itemPath, "搜索字段不存在", SeverityError)
		}
		if exists && textOnly && field.Type != FieldString {
			v.add("vector_field_type_invalid", itemPath, "语义搜索字段必须是 string", SeverityError)
		}
		if seen[value] {
			v.add("search_field_duplicate", itemPath, "搜索字段重复", SeverityError)
		}
		seen[value] = true
	}
}

func (v *draftValidator) validateRuleExpressions(path string, rules []RuleExpression) {
	seen := map[string]bool{}
	for i, rule := range rules {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if !validIdentifier(rule.ID) {
			v.add("rule_id_invalid", itemPath+".id", "规则 ID 必须是小写 snake_case", SeverityError)
		}
		if seen[rule.ID] {
			v.add("rule_id_duplicate", itemPath+".id", "规则 ID 重复", SeverityError)
		}
		if strings.TrimSpace(rule.Description) == "" || strings.TrimSpace(rule.Expression) == "" {
			v.add("rule_definition_incomplete", itemPath, "规则说明和表达式不能为空", v.requiredSeverity())
		}
		seen[rule.ID] = true
	}
}

func (v *draftValidator) validateDecisions() {
	byPath := map[string]RuleDecision{}
	for i, decision := range v.draft.Rules.Decisions {
		path := fmt.Sprintf("rules.decisions[%d]", i)
		if strings.TrimSpace(decision.Path) == "" || strings.TrimSpace(decision.Reason) == "" {
			v.add("decision_incomplete", path, "规则决策必须包含路径和理由", v.requiredSeverity())
		}
		if len(bytes.TrimSpace(decision.Value)) == 0 || !json.Valid(decision.Value) {
			v.add("decision_value_invalid", path+".value", "规则决策值必须是合法 JSON", SeverityError)
		}
		if !validDecisionSource(decision.Source) || (decision.Risk != RiskLow && decision.Risk != RiskHigh) {
			v.add("decision_metadata_invalid", path, "规则决策来源或风险等级非法", SeverityError)
		}
		if decision.Confidence < 0 || decision.Confidence > 1 {
			v.add("decision_confidence_invalid", path+".confidence", "置信度必须在 0..1 之间", SeverityError)
		}
		if _, exists := byPath[decision.Path]; exists {
			v.add("decision_path_duplicate", path+".path", "同一路径只能有一条当前决策", SeverityError)
		}
		if decision.Source == DecisionObserved || decision.Source == DecisionInferred {
			if len(decision.Evidence) == 0 {
				v.add("decision_evidence_required", path+".evidence",
					"观察或推断出的规则决策必须携带可追溯证据", v.requiredSeverity())
			}
			for evidenceIndex, evidence := range decision.Evidence {
				if strings.TrimSpace(evidence.ResourceID) == "" || strings.TrimSpace(evidence.Location) == "" {
					v.add("decision_evidence_invalid", fmt.Sprintf("%s.evidence[%d]", path, evidenceIndex),
						"规则证据必须包含资源和位置", SeverityError)
				}
			}
		}
		if decision.Risk == RiskHigh &&
			(strings.TrimSpace(decision.ConfirmedBy) == "" || decision.ConfirmedAt.IsZero()) {
			v.add("high_risk_decision_unconfirmed", path,
				"高风险业务决策必须记录确认人和确认时间", v.requiredSeverity())
		}
		byPath[decision.Path] = decision
	}
	if v.draft.Rules.DataContract != nil {
		v.requireConfirmedDecision(byPath, "rules.data_contract.record_granularity")
		v.requireConfirmedDecision(byPath, "rules.data_contract.key_fields")
	}
}

func (v *draftValidator) requireConfirmedDecision(decisions map[string]RuleDecision, path string) {
	decision, exists := decisions[path]
	if !exists || decision.Risk != RiskHigh || strings.TrimSpace(decision.ConfirmedBy) == "" || decision.ConfirmedAt.IsZero() {
		v.add("business_decision_required", path,
			"记录粒度和唯一键必须形成已确认的高风险业务决策", v.requiredSeverity())
	}
}

func (v *draftValidator) validateAcceptanceCases() {
	seen := map[string]bool{}
	for i, testCase := range v.draft.AcceptanceCases {
		path := fmt.Sprintf("acceptance_cases[%d]", i)
		if !validIdentifier(testCase.ID) || strings.TrimSpace(testCase.Name) == "" {
			v.add("acceptance_case_identity_invalid", path, "验收用例 ID 或名称非法", SeverityError)
		}
		if seen[testCase.ID] {
			v.add("acceptance_case_duplicate", path+".id", "验收用例 ID 重复", SeverityError)
		}
		if !json.Valid(testCase.Input) || !json.Valid(testCase.Expectation) {
			v.add("acceptance_case_payload_invalid", path, "验收用例输入和预期必须是合法 JSON", SeverityError)
		}
		seen[testCase.ID] = true
	}
	if v.mode == ValidatePublish {
		if len(v.draft.AcceptanceCases) == 0 {
			v.add("acceptance_case_required", "acceptance_cases",
				"发布前至少需要一个通过的端到端验收用例", SeverityError)
		}
		for i, testCase := range v.draft.AcceptanceCases {
			if !testCase.LastPassed {
				v.add("acceptance_case_not_passed", fmt.Sprintf("acceptance_cases[%d]", i),
					"发布前所有验收用例必须通过", SeverityError)
			}
		}
	}
}

type resolvedEndpoint struct {
	resourceType ResourceType
	role         PortRole
	multiple     bool
}

func (v *draftValidator) resolveSource(endpoint Endpoint, path string) (resolvedEndpoint, bool) {
	switch endpoint.Kind {
	case EndpointWorkflowInput:
		if endpoint.NodeID != "" {
			v.add("endpoint_node_forbidden", path, "流程输入端点不能包含 node_id", SeverityError)
			return resolvedEndpoint{}, false
		}
		port, exists := v.inputs[endpoint.Port]
		if !exists {
			v.add("workflow_input_not_found", path, "连接引用了不存在的流程输入", SeverityError)
			return resolvedEndpoint{}, false
		}
		return resolvedEndpoint{resourceType: port.ResourceType}, true
	case EndpointNodeOutput:
		capability, exists := v.capabilities[endpoint.NodeID]
		if !exists {
			v.add("connection_node_not_found", path, "连接引用了不存在或无效的节点", SeverityError)
			return resolvedEndpoint{}, false
		}
		port, exists := findPort(capability.Outputs, endpoint.Port)
		if !exists {
			v.add("node_output_not_found", path, "连接引用了不存在的节点输出端口", SeverityError)
			return resolvedEndpoint{}, false
		}
		return resolvedEndpoint{resourceType: port.ResourceType, role: port.Role, multiple: port.Multiple}, true
	default:
		v.add("connection_source_kind_invalid", path, "连接来源只能是流程输入或节点输出", SeverityError)
		return resolvedEndpoint{}, false
	}
}

func (v *draftValidator) resolveTarget(endpoint Endpoint, path string) (resolvedEndpoint, bool) {
	switch endpoint.Kind {
	case EndpointNodeInput:
		capability, exists := v.capabilities[endpoint.NodeID]
		if !exists {
			v.add("connection_node_not_found", path, "连接引用了不存在或无效的节点", SeverityError)
			return resolvedEndpoint{}, false
		}
		port, exists := findPort(capability.Inputs, endpoint.Port)
		if !exists {
			v.add("node_input_not_found", path, "连接引用了不存在的节点输入端口", SeverityError)
			return resolvedEndpoint{}, false
		}
		return resolvedEndpoint{resourceType: port.ResourceType, role: port.Role, multiple: port.Multiple}, true
	case EndpointWorkflowOutput:
		if endpoint.NodeID != "" {
			v.add("endpoint_node_forbidden", path, "流程输出端点不能包含 node_id", SeverityError)
			return resolvedEndpoint{}, false
		}
		port, exists := v.outputs[endpoint.Port]
		if !exists {
			v.add("workflow_output_not_found", path, "连接引用了不存在的流程输出", SeverityError)
			return resolvedEndpoint{}, false
		}
		return resolvedEndpoint{resourceType: port.ResourceType}, true
	default:
		v.add("connection_target_kind_invalid", path, "连接目标只能是节点输入或流程输出", SeverityError)
		return resolvedEndpoint{}, false
	}
}

func (v *draftValidator) requiredSeverity() IssueSeverity {
	if v.mode == ValidatePublish {
		return SeverityError
	}
	return SeverityWarning
}

func (v *draftValidator) add(code, path, message string, severity IssueSeverity) {
	v.issues = append(v.issues, ValidationIssue{Code: code, Path: path, Message: message, Severity: severity})
}

func validateWorkflowPort(port WorkflowPort) error {
	if !validIdentifier(port.Name) {
		return fmt.Errorf("端口名 %q 非法", port.Name)
	}
	if strings.TrimSpace(port.Label) == "" || strings.TrimSpace(string(port.ResourceType)) == "" {
		return fmt.Errorf("端口 %s 缺少名称或资源类型", port.Name)
	}
	return nil
}

func endpointKey(endpoint Endpoint) string {
	return fmt.Sprintf("%s:%s:%s", endpoint.Kind, endpoint.NodeID, endpoint.Port)
}

func findPort(ports []PortDefinition, name string) (PortDefinition, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return PortDefinition{}, false
}

func hasRuleSection(bundle RuleBundle, section RuleSection) bool {
	switch section {
	case RuleDataContract:
		return bundle.DataContract != nil
	case RuleExtraction:
		return bundle.Extraction != nil
	case RuleSearch:
		return bundle.Search != nil
	case RuleOutputContract:
		return bundle.OutputContract != nil
	default:
		return false
	}
}

func validFieldType(value FieldType) bool {
	switch value {
	case FieldString, FieldInteger, FieldNumber, FieldBoolean, FieldDate, FieldDateTime, FieldObject, FieldArray:
		return true
	default:
		return false
	}
}

func validDecisionSource(value DecisionSource) bool {
	switch value {
	case DecisionObserved, DecisionInferred, DecisionPreset, DecisionUserConfirmed, DecisionUserDefined:
		return true
	default:
		return false
	}
}
