package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	domain "reqflow/internal/domain/workflow"
)

type DraftEditor struct {
	catalog domain.CapabilityCatalog
}

func NewDraftEditor(catalog domain.CapabilityCatalog) (*DraftEditor, error) {
	if catalog == nil {
		return nil, fmt.Errorf("workflow draft editor: capability catalog is required")
	}
	return &DraftEditor{catalog: catalog}, nil
}

type InsertBetweenCommand struct {
	Connection domain.Connection   `json:"connection"`
	Node       domain.WorkflowNode `json:"node"`
	// SideInputs maps a required side-input port on the new node to an existing
	// workflow input port. The command is atomic: missing side inputs reject the
	// insertion instead of leaving a broken draft.
	SideInputs map[string]string `json:"side_inputs,omitempty"`
}

type CreateFromBlankCommand struct {
	Node       domain.WorkflowNode `json:"node"`
	InputPort  string              `json:"input_port"`
	OutputPort string              `json:"output_port"`
	SideInputs map[string]string   `json:"side_inputs,omitempty"`
}

func (e *DraftEditor) CreateFromBlank(draft domain.WorkflowDraft, command CreateFromBlankCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	if len(next.Nodes) != 0 || len(next.Connections) != 0 {
		return draft, fmt.Errorf("只有空白草稿才能创建首个节点")
	}
	if err := ensureNewNode(next, command.Node); err != nil {
		return draft, err
	}
	capability, exists := e.catalog.Lookup(command.Node.Capability)
	if !exists {
		return draft, fmt.Errorf("首节点 Capability 未注册")
	}
	input, ok := findDefinitionPort(capability.Inputs, command.InputPort)
	if !ok || input.Role != domain.PortPrimary {
		return draft, fmt.Errorf("首节点主输入端口不存在")
	}
	output, ok := findDefinitionPort(capability.Outputs, command.OutputPort)
	if !ok || output.Role != domain.PortPrimary {
		return draft, fmt.Errorf("首节点主输出端口不存在")
	}
	inputType, err := e.sourceType(next, domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: command.InputPort})
	if err != nil || inputType != input.ResourceType {
		return draft, fmt.Errorf("首节点主输入与流程输入类型不兼容")
	}
	outputType, err := e.targetType(next, domain.Endpoint{Kind: domain.EndpointWorkflowOutput, Port: command.OutputPort})
	if err != nil || outputType != output.ResourceType {
		return draft, fmt.Errorf("首节点主输出与流程输出类型不兼容")
	}
	if err := domain.ValidateCapabilityConfig(capability, command.Node.Config); err != nil {
		return draft, err
	}
	connections := []domain.Connection{{
		From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: command.InputPort},
		To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.Node.ID, Port: input.Name},
	}, {
		From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: command.Node.ID, Port: output.Name},
		To:   domain.Endpoint{Kind: domain.EndpointWorkflowOutput, Port: command.OutputPort},
	}}
	connections, err = e.bindRequiredSideInputs(next, connections, command.Node.ID, capability, command.SideInputs)
	if err != nil {
		return draft, err
	}
	next.Nodes = []domain.WorkflowNode{command.Node}
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, command.Node.ID); err != nil {
		return draft, err
	}
	return next, nil
}

func (e *DraftEditor) InsertBetween(draft domain.WorkflowDraft, command InsertBetweenCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	if command.Connection.From.Kind != domain.EndpointNodeOutput || command.Connection.To.Kind != domain.EndpointNodeInput {
		return draft, fmt.Errorf("首期只能在两个相邻节点的主连接之间插入")
	}
	if !containsConnection(next.Connections, command.Connection) {
		return draft, fmt.Errorf("待切开的连接不存在")
	}
	for _, node := range next.Nodes {
		if node.ID == command.Node.ID {
			return draft, fmt.Errorf("节点 ID %s 已存在", command.Node.ID)
		}
	}
	capability, exists := e.catalog.Lookup(command.Node.Capability)
	if !exists {
		return draft, fmt.Errorf("待插入节点引用了未注册的 Capability")
	}
	primaryInput, ok := primaryPort(capability.Inputs)
	if !ok {
		return draft, fmt.Errorf("待插入 Capability 缺少唯一主输入")
	}
	primaryOutput, ok := primaryPort(capability.Outputs)
	if !ok {
		return draft, fmt.Errorf("待插入 Capability 缺少唯一主输出")
	}

	sourceType, err := e.sourceType(next, command.Connection.From)
	if err != nil {
		return draft, err
	}
	targetType, err := e.targetType(next, command.Connection.To)
	if err != nil {
		return draft, err
	}
	if sourceType != primaryInput.ResourceType || primaryOutput.ResourceType != targetType {
		return draft, fmt.Errorf("Capability %s 不能桥接当前连接：%s → %s → %s → %s",
			command.Node.Capability.Kind, sourceType, primaryInput.ResourceType, primaryOutput.ResourceType, targetType)
	}

	connections := make([]domain.Connection, 0, len(next.Connections)+1+len(command.SideInputs))
	for _, connection := range next.Connections {
		if sameConnection(connection, command.Connection) {
			continue
		}
		connections = append(connections, connection)
	}
	connections = append(connections,
		domain.Connection{From: command.Connection.From,
			To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.Node.ID, Port: primaryInput.Name}},
		domain.Connection{From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: command.Node.ID, Port: primaryOutput.Name},
			To: command.Connection.To},
	)
	for _, port := range capability.Inputs {
		if port.Role != domain.PortSide || !port.Required {
			continue
		}
		workflowInput := strings.TrimSpace(command.SideInputs[port.Name])
		if workflowInput == "" {
			return draft, fmt.Errorf("待插入节点缺少侧输入 %s 的流程输入绑定", port.Label)
		}
		connections = append(connections, domain.Connection{
			From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: workflowInput},
			To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.Node.ID, Port: port.Name},
		})
	}
	next.Nodes = append(next.Nodes, command.Node)
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, command.Node.ID); err != nil {
		return draft, err
	}
	return next, nil
}

type RemoveAndBridgeCommand struct {
	NodeID string `json:"node_id"`
}

func (e *DraftEditor) RemoveAndBridge(draft domain.WorkflowDraft, command RemoveAndBridgeCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	if len(next.Nodes) <= 1 {
		return draft, fmt.Errorf("不能删除线性流程的最后一个节点")
	}
	nodeIndex := -1
	var capability domain.CapabilityDefinition
	for i, node := range next.Nodes {
		if node.ID != command.NodeID {
			continue
		}
		nodeIndex = i
		var exists bool
		capability, exists = e.catalog.Lookup(node.Capability)
		if !exists {
			return draft, fmt.Errorf("节点 Capability 不存在")
		}
		break
	}
	if nodeIndex < 0 {
		return draft, fmt.Errorf("节点 %s 不存在", command.NodeID)
	}
	primaryInput, _ := primaryPort(capability.Inputs)
	primaryOutput, _ := primaryPort(capability.Outputs)
	var incoming, outgoing *domain.Connection
	for i := range next.Connections {
		connection := &next.Connections[i]
		if connection.To.Kind == domain.EndpointNodeInput && connection.To.NodeID == command.NodeID &&
			connection.To.Port == primaryInput.Name {
			copy := *connection
			incoming = &copy
		}
		if connection.From.Kind == domain.EndpointNodeOutput && connection.From.NodeID == command.NodeID {
			if connection.To.Kind == domain.EndpointWorkflowOutput {
				return draft, fmt.Errorf("节点仍暴露流程输出 %s，请先取消该输出", connection.To.Port)
			}
			if connection.From.Port == primaryOutput.Name && connection.To.Kind == domain.EndpointNodeInput {
				copy := *connection
				outgoing = &copy
			}
		}
	}
	if incoming == nil || outgoing == nil {
		return draft, fmt.Errorf("首期只支持删除具有明确前驱和后继的中间节点")
	}
	sourceType, err := e.sourceType(next, incoming.From)
	if err != nil {
		return draft, err
	}
	targetType, err := e.targetType(next, outgoing.To)
	if err != nil {
		return draft, err
	}
	if sourceType != targetType {
		return draft, fmt.Errorf("删除后无法桥接：前驱输出 %s 与后继输入 %s 不兼容", sourceType, targetType)
	}

	next.Nodes = append(next.Nodes[:nodeIndex], next.Nodes[nodeIndex+1:]...)
	connections := make([]domain.Connection, 0, len(next.Connections)-1)
	for _, connection := range next.Connections {
		if endpointUsesNode(connection.From, command.NodeID) || endpointUsesNode(connection.To, command.NodeID) {
			continue
		}
		connections = append(connections, connection)
	}
	connections = append(connections, domain.Connection{From: incoming.From, To: outgoing.To})
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, ""); err != nil {
		return draft, err
	}
	return next, nil
}

type AppendAfterCommand struct {
	AfterNodeID string              `json:"after_node_id"`
	Node        domain.WorkflowNode `json:"node"`
	SideInputs  map[string]string   `json:"side_inputs,omitempty"`
}

func (e *DraftEditor) AppendAfter(draft domain.WorkflowDraft, command AppendAfterCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	if err := ensureNewNode(next, command.Node); err != nil {
		return draft, err
	}
	capability, exists := e.catalog.Lookup(command.Node.Capability)
	if !exists {
		return draft, fmt.Errorf("待追加节点引用了未注册的 Capability")
	}
	input, ok := primaryPort(capability.Inputs)
	if !ok {
		return draft, fmt.Errorf("待追加 Capability 缺少唯一主输入")
	}
	output, ok := primaryPort(capability.Outputs)
	if !ok {
		return draft, fmt.Errorf("待追加 Capability 缺少唯一主输出")
	}
	previous, previousCapability, err := e.node(next, command.AfterNodeID)
	if err != nil {
		return draft, err
	}
	previousOutput, _ := primaryPort(previousCapability.Outputs)
	if previousOutput.ResourceType != input.ResourceType {
		return draft, fmt.Errorf("追加节点主输入 %s 不能消费当前尾节点输出 %s", input.ResourceType, previousOutput.ResourceType)
	}
	for _, connection := range next.Connections {
		if connection.From.Kind == domain.EndpointNodeOutput && connection.From.NodeID == previous.ID &&
			connection.From.Port == previousOutput.Name && connection.To.Kind == domain.EndpointNodeInput {
			return draft, fmt.Errorf("节点 %s 不是当前线性流程尾节点", previous.ID)
		}
	}
	connections := append([]domain.Connection(nil), next.Connections...)
	movedOutput := false
	for index := range connections {
		connection := &connections[index]
		if connection.From.Kind != domain.EndpointNodeOutput || connection.From.NodeID != previous.ID ||
			connection.From.Port != previousOutput.Name || connection.To.Kind != domain.EndpointWorkflowOutput {
			continue
		}
		connection.From = domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: command.Node.ID, Port: output.Name}
		for outputIndex := range next.Outputs {
			if next.Outputs[outputIndex].Name == connection.To.Port {
				next.Outputs[outputIndex].ResourceType = output.ResourceType
			}
		}
		movedOutput = true
	}
	if !movedOutput {
		return draft, fmt.Errorf("当前尾节点的主输出没有暴露为流程输出")
	}
	connections = append(connections, domain.Connection{
		From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: previous.ID, Port: previousOutput.Name},
		To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.Node.ID, Port: input.Name},
	})
	connections, err = e.bindRequiredSideInputs(next, connections, command.Node.ID, capability, command.SideInputs)
	if err != nil {
		return draft, err
	}
	next.Nodes = append(next.Nodes, command.Node)
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, command.Node.ID); err != nil {
		return draft, err
	}
	return next, nil
}

type PrependBeforeCommand struct {
	BeforeNodeID string              `json:"before_node_id"`
	Node         domain.WorkflowNode `json:"node"`
	SideInputs   map[string]string   `json:"side_inputs,omitempty"`
}

func (e *DraftEditor) PrependBefore(draft domain.WorkflowDraft, command PrependBeforeCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	if err := ensureNewNode(next, command.Node); err != nil {
		return draft, err
	}
	capability, exists := e.catalog.Lookup(command.Node.Capability)
	if !exists {
		return draft, fmt.Errorf("待前置节点引用了未注册的 Capability")
	}
	input, _ := primaryPort(capability.Inputs)
	output, _ := primaryPort(capability.Outputs)
	nextNode, nextCapability, err := e.node(next, command.BeforeNodeID)
	if err != nil {
		return draft, err
	}
	nextInput, _ := primaryPort(nextCapability.Inputs)
	if output.ResourceType != nextInput.ResourceType {
		return draft, fmt.Errorf("前置节点主输出 %s 不能供给当前头节点输入 %s", output.ResourceType, nextInput.ResourceType)
	}
	connections := append([]domain.Connection(nil), next.Connections...)
	movedInput := false
	for index := range connections {
		connection := &connections[index]
		if connection.To.Kind != domain.EndpointNodeInput || connection.To.NodeID != nextNode.ID ||
			connection.To.Port != nextInput.Name {
			continue
		}
		if connection.From.Kind != domain.EndpointWorkflowInput {
			return draft, fmt.Errorf("节点 %s 不是当前线性流程头节点", nextNode.ID)
		}
		for inputIndex := range next.Inputs {
			if next.Inputs[inputIndex].Name == connection.From.Port {
				next.Inputs[inputIndex].ResourceType = input.ResourceType
			}
		}
		connection.To = domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.Node.ID, Port: input.Name}
		movedInput = true
	}
	if !movedInput {
		return draft, fmt.Errorf("当前头节点的主输入没有连接流程输入")
	}
	connections = append(connections, domain.Connection{
		From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: command.Node.ID, Port: output.Name},
		To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: nextNode.ID, Port: nextInput.Name},
	})
	connections, err = e.bindRequiredSideInputs(next, connections, command.Node.ID, capability, command.SideInputs)
	if err != nil {
		return draft, err
	}
	next.Nodes = append(next.Nodes, command.Node)
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, command.Node.ID); err != nil {
		return draft, err
	}
	return next, nil
}

type ReplaceNodeCommand struct {
	Node          domain.WorkflowNode `json:"node"`
	InputPortMap  map[string]string   `json:"input_port_map,omitempty"`
	OutputPortMap map[string]string   `json:"output_port_map,omitempty"`
}

func (e *DraftEditor) ReplaceNode(draft domain.WorkflowDraft, command ReplaceNodeCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	index := -1
	var existing domain.WorkflowNode
	for nodeIndex, node := range next.Nodes {
		if node.ID == command.Node.ID {
			index, existing = nodeIndex, node
			break
		}
	}
	if index < 0 {
		return draft, fmt.Errorf("节点 %s 不存在", command.Node.ID)
	}
	oldCapability, exists := e.catalog.Lookup(existing.Capability)
	if !exists {
		return draft, fmt.Errorf("原节点 Capability 不存在")
	}
	newCapability, exists := e.catalog.Lookup(command.Node.Capability)
	if !exists {
		return draft, fmt.Errorf("替换节点 Capability 不存在")
	}
	if err := domain.ValidateCapabilityConfig(newCapability, command.Node.Config); err != nil {
		return draft, err
	}
	connections := append([]domain.Connection(nil), next.Connections...)
	for connectionIndex := range connections {
		connection := &connections[connectionIndex]
		if connection.To.Kind == domain.EndpointNodeInput && connection.To.NodeID == existing.ID {
			oldPort, ok := findDefinitionPort(oldCapability.Inputs, connection.To.Port)
			if !ok {
				return draft, fmt.Errorf("原输入端口 %s 不存在", connection.To.Port)
			}
			newName := mappedPort(command.InputPortMap, oldPort.Name)
			newPort, ok := findDefinitionPort(newCapability.Inputs, newName)
			if !ok || oldPort.ResourceType != newPort.ResourceType || oldPort.Role != newPort.Role {
				return draft, fmt.Errorf("替换节点输入端口 %s 无兼容映射", oldPort.Name)
			}
			connection.To.Port = newName
		}
		if connection.From.Kind == domain.EndpointNodeOutput && connection.From.NodeID == existing.ID {
			oldPort, ok := findDefinitionPort(oldCapability.Outputs, connection.From.Port)
			if !ok {
				return draft, fmt.Errorf("原输出端口 %s 不存在", connection.From.Port)
			}
			newName := mappedPort(command.OutputPortMap, oldPort.Name)
			newPort, ok := findDefinitionPort(newCapability.Outputs, newName)
			if !ok || oldPort.ResourceType != newPort.ResourceType || oldPort.Role != newPort.Role {
				return draft, fmt.Errorf("替换节点输出端口 %s 无兼容映射", oldPort.Name)
			}
			connection.From.Port = newName
		}
	}
	for _, port := range newCapability.Inputs {
		if port.Required && !hasNodeInputConnection(connections, existing.ID, port.Name) {
			return draft, fmt.Errorf("替换节点缺少必填输入 %s", port.Label)
		}
	}
	next.Nodes[index] = command.Node
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, command.Node.ID); err != nil {
		return draft, err
	}
	return next, nil
}

type SetNodeConfigCommand struct {
	NodeID string          `json:"node_id"`
	Config json.RawMessage `json:"config"`
}

func (e *DraftEditor) SetNodeConfig(draft domain.WorkflowDraft, command SetNodeConfigCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	for index := range next.Nodes {
		if next.Nodes[index].ID != command.NodeID {
			continue
		}
		capability, exists := e.catalog.Lookup(next.Nodes[index].Capability)
		if !exists {
			return draft, fmt.Errorf("节点 Capability 不存在")
		}
		if err := domain.ValidateCapabilityConfig(capability, command.Config); err != nil {
			return draft, err
		}
		next.Nodes[index].Config = append(json.RawMessage(nil), command.Config...)
		invalidateAcceptance(&next)
		next.Revision++
		return next, nil
	}
	return draft, fmt.Errorf("节点 %s 不存在", command.NodeID)
}

type BindSideInputCommand struct {
	NodeID        string `json:"node_id"`
	NodePort      string `json:"node_port"`
	WorkflowInput string `json:"workflow_input"`
}

func (e *DraftEditor) BindSideInput(draft domain.WorkflowDraft, command BindSideInputCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	_, capability, err := e.node(next, command.NodeID)
	if err != nil {
		return draft, err
	}
	port, ok := findDefinitionPort(capability.Inputs, command.NodePort)
	if !ok || port.Role != domain.PortSide {
		return draft, fmt.Errorf("节点侧输入端口不存在")
	}
	inputType, err := e.sourceType(next, domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: command.WorkflowInput})
	if err != nil || inputType != port.ResourceType {
		return draft, fmt.Errorf("流程输入与节点侧输入类型不兼容")
	}
	connections := make([]domain.Connection, 0, len(next.Connections)+1)
	for _, connection := range next.Connections {
		if connection.To.Kind == domain.EndpointNodeInput && connection.To.NodeID == command.NodeID && connection.To.Port == command.NodePort {
			continue
		}
		connections = append(connections, connection)
	}
	connections = append(connections, domain.Connection{
		From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: command.WorkflowInput},
		To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: command.NodeID, Port: command.NodePort},
	})
	next.Connections = connections
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, ""); err != nil {
		return draft, err
	}
	return next, nil
}

type SetWorkflowPortCommand struct {
	Direction domain.EndpointKind `json:"direction"`
	Port      domain.WorkflowPort `json:"port"`
}

func (e *DraftEditor) SetWorkflowPort(draft domain.WorkflowDraft, command SetWorkflowPortCommand) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	ports := &next.Inputs
	if command.Direction == domain.EndpointWorkflowOutput {
		ports = &next.Outputs
	} else if command.Direction != domain.EndpointWorkflowInput {
		return draft, fmt.Errorf("流程端口方向非法")
	}
	found := false
	for index := range *ports {
		if (*ports)[index].Name == command.Port.Name {
			(*ports)[index] = command.Port
			found = true
			break
		}
	}
	if !found {
		*ports = append(*ports, command.Port)
	}
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, ""); err != nil {
		return draft, err
	}
	return next, nil
}

func (e *DraftEditor) SetDataContract(draft domain.WorkflowDraft, contract domain.DataContract) (domain.WorkflowDraft, error) {
	return e.setRuleBundle(draft, func(next *domain.WorkflowDraft) {
		next.Rules.DataContract = &contract
		invalidateConfirmations(next, "rules.data_contract.")
	})
}

func (e *DraftEditor) SetExtractionSpec(draft domain.WorkflowDraft, spec domain.ExtractionSpec) (domain.WorkflowDraft, error) {
	return e.setRuleBundle(draft, func(next *domain.WorkflowDraft) { next.Rules.Extraction = &spec })
}

func (e *DraftEditor) SetSearchSpec(draft domain.WorkflowDraft, spec domain.SearchSpec) (domain.WorkflowDraft, error) {
	return e.setRuleBundle(draft, func(next *domain.WorkflowDraft) { next.Rules.Search = &spec })
}

func (e *DraftEditor) SetOutputContract(draft domain.WorkflowDraft, contract domain.OutputContract) (domain.WorkflowDraft, error) {
	return e.setRuleBundle(draft, func(next *domain.WorkflowDraft) { next.Rules.OutputContract = &contract })
}

func (e *DraftEditor) setRuleBundle(draft domain.WorkflowDraft, mutate func(*domain.WorkflowDraft)) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	mutate(&next)
	invalidateAcceptance(&next)
	next.Revision++
	if err := validateEditResult(next, e.catalog, ""); err != nil {
		return draft, err
	}
	return next, nil
}

type ConfirmDecisionCommand struct {
	Path      string          `json:"path"`
	Value     json.RawMessage `json:"value"`
	ActorID   string          `json:"-"`
	Confirmed time.Time       `json:"-"`
}

func (e *DraftEditor) ConfirmDecision(draft domain.WorkflowDraft, command ConfirmDecisionCommand) (domain.WorkflowDraft, error) {
	if strings.TrimSpace(command.ActorID) == "" || command.Confirmed.IsZero() || !json.Valid(command.Value) {
		return draft, fmt.Errorf("确认人、确认时间和值必须有效")
	}
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	found := false
	for index := range next.Rules.Decisions {
		decision := &next.Rules.Decisions[index]
		if decision.Path != command.Path {
			continue
		}
		if string(decision.Value) != string(command.Value) {
			return draft, fmt.Errorf("确认值与当前决策值不一致")
		}
		decision.Source = domain.DecisionUserConfirmed
		decision.Risk = domain.RiskHigh
		decision.Confidence = 1
		decision.ConfirmedBy = command.ActorID
		decision.ConfirmedAt = command.Confirmed
		found = true
		break
	}
	if !found {
		return draft, fmt.Errorf("待确认决策不存在")
	}
	next.Revision++
	return next, nil
}

func (e *DraftEditor) UpsertAcceptanceCase(draft domain.WorkflowDraft, acceptance domain.AcceptanceCase) (domain.WorkflowDraft, error) {
	next, err := cloneDraft(draft)
	if err != nil {
		return draft, err
	}
	acceptance.LastPassed = false
	acceptance.LastPassedRevision = 0
	acceptance.LastPreviewID = ""
	acceptance.LastRunAt = time.Time{}
	found := false
	for index := range next.AcceptanceCases {
		if next.AcceptanceCases[index].ID == acceptance.ID {
			next.AcceptanceCases[index] = acceptance
			found = true
			break
		}
	}
	if !found {
		next.AcceptanceCases = append(next.AcceptanceCases, acceptance)
	}
	next.Revision++
	issues := domain.Validate(next, e.catalog, domain.ValidateDraft)
	if domain.HasErrors(issues) {
		return draft, fmt.Errorf("验收用例非法")
	}
	return next, nil
}

func (e *DraftEditor) sourceType(draft domain.WorkflowDraft, endpoint domain.Endpoint) (domain.ResourceType, error) {
	switch endpoint.Kind {
	case domain.EndpointWorkflowInput:
		for _, port := range draft.Inputs {
			if port.Name == endpoint.Port {
				return port.ResourceType, nil
			}
		}
	case domain.EndpointNodeOutput:
		for _, node := range draft.Nodes {
			if node.ID != endpoint.NodeID {
				continue
			}
			capability, exists := e.catalog.Lookup(node.Capability)
			if !exists {
				break
			}
			for _, port := range capability.Outputs {
				if port.Name == endpoint.Port {
					return port.ResourceType, nil
				}
			}
		}
	}
	return "", fmt.Errorf("无法解析连接来源")
}

func (e *DraftEditor) targetType(draft domain.WorkflowDraft, endpoint domain.Endpoint) (domain.ResourceType, error) {
	switch endpoint.Kind {
	case domain.EndpointWorkflowOutput:
		for _, port := range draft.Outputs {
			if port.Name == endpoint.Port {
				return port.ResourceType, nil
			}
		}
	case domain.EndpointNodeInput:
		for _, node := range draft.Nodes {
			if node.ID != endpoint.NodeID {
				continue
			}
			capability, exists := e.catalog.Lookup(node.Capability)
			if !exists {
				break
			}
			for _, port := range capability.Inputs {
				if port.Name == endpoint.Port {
					return port.ResourceType, nil
				}
			}
		}
	}
	return "", fmt.Errorf("无法解析连接目标")
}

func validateEditResult(draft domain.WorkflowDraft, catalog domain.CapabilityCatalog, _ string) error {
	issues := domain.Validate(draft, catalog, domain.ValidateDraft)
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			return fmt.Errorf("编辑后流程非法 [%s] %s", issue.Code, issue.Message)
		}
	}
	return nil
}

func ensureNewNode(draft domain.WorkflowDraft, node domain.WorkflowNode) error {
	for _, existing := range draft.Nodes {
		if existing.ID == node.ID {
			return fmt.Errorf("节点 ID %s 已存在", node.ID)
		}
	}
	return nil
}

func (e *DraftEditor) node(draft domain.WorkflowDraft, nodeID string) (domain.WorkflowNode, domain.CapabilityDefinition, error) {
	for _, node := range draft.Nodes {
		if node.ID != nodeID {
			continue
		}
		capability, exists := e.catalog.Lookup(node.Capability)
		if !exists {
			return domain.WorkflowNode{}, domain.CapabilityDefinition{}, fmt.Errorf("节点 Capability 不存在")
		}
		return node, capability, nil
	}
	return domain.WorkflowNode{}, domain.CapabilityDefinition{}, fmt.Errorf("节点 %s 不存在", nodeID)
}

func (e *DraftEditor) bindRequiredSideInputs(draft domain.WorkflowDraft, connections []domain.Connection,
	nodeID string, capability domain.CapabilityDefinition, bindings map[string]string) ([]domain.Connection, error) {
	for _, port := range capability.Inputs {
		if port.Role != domain.PortSide || !port.Required {
			continue
		}
		workflowInput := strings.TrimSpace(bindings[port.Name])
		if workflowInput == "" {
			return nil, fmt.Errorf("节点缺少侧输入 %s 的流程输入绑定", port.Label)
		}
		sourceType, err := e.sourceType(draft, domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: workflowInput})
		if err != nil || sourceType != port.ResourceType {
			return nil, fmt.Errorf("侧输入 %s 的流程输入类型不兼容", port.Label)
		}
		connections = append(connections, domain.Connection{
			From: domain.Endpoint{Kind: domain.EndpointWorkflowInput, Port: workflowInput},
			To:   domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: nodeID, Port: port.Name},
		})
	}
	return connections, nil
}

func findDefinitionPort(ports []domain.PortDefinition, name string) (domain.PortDefinition, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return domain.PortDefinition{}, false
}

func mappedPort(mapping map[string]string, oldName string) string {
	if mapped := strings.TrimSpace(mapping[oldName]); mapped != "" {
		return mapped
	}
	return oldName
}

func hasNodeInputConnection(connections []domain.Connection, nodeID, port string) bool {
	for _, connection := range connections {
		if connection.To.Kind == domain.EndpointNodeInput && connection.To.NodeID == nodeID && connection.To.Port == port {
			return true
		}
	}
	return false
}

func invalidateAcceptance(draft *domain.WorkflowDraft) {
	for index := range draft.AcceptanceCases {
		draft.AcceptanceCases[index].LastPassed = false
		draft.AcceptanceCases[index].LastPassedRevision = 0
		draft.AcceptanceCases[index].LastPreviewID = ""
		draft.AcceptanceCases[index].LastRunAt = time.Time{}
	}
}

func invalidateConfirmations(draft *domain.WorkflowDraft, prefix string) {
	for index := range draft.Rules.Decisions {
		if strings.HasPrefix(draft.Rules.Decisions[index].Path, prefix) {
			draft.Rules.Decisions[index].ConfirmedBy = ""
			draft.Rules.Decisions[index].ConfirmedAt = time.Time{}
		}
	}
}

func primaryPort(ports []domain.PortDefinition) (domain.PortDefinition, bool) {
	var result domain.PortDefinition
	found := false
	for _, port := range ports {
		if port.Role != domain.PortPrimary {
			continue
		}
		if found {
			return domain.PortDefinition{}, false
		}
		result, found = port, true
	}
	return result, found
}

func containsConnection(connections []domain.Connection, target domain.Connection) bool {
	for _, connection := range connections {
		if sameConnection(connection, target) {
			return true
		}
	}
	return false
}

func sameConnection(left, right domain.Connection) bool {
	return left.From == right.From && left.To == right.To
}

func endpointUsesNode(endpoint domain.Endpoint, nodeID string) bool {
	return endpoint.NodeID == nodeID && (endpoint.Kind == domain.EndpointNodeInput || endpoint.Kind == domain.EndpointNodeOutput)
}

func cloneDraft(draft domain.WorkflowDraft) (domain.WorkflowDraft, error) {
	raw, err := json.Marshal(draft)
	if err != nil {
		return domain.WorkflowDraft{}, fmt.Errorf("复制 Workflow Draft: %w", err)
	}
	var clone domain.WorkflowDraft
	if err := json.Unmarshal(raw, &clone); err != nil {
		return domain.WorkflowDraft{}, fmt.Errorf("复制 Workflow Draft: %w", err)
	}
	return clone, nil
}
