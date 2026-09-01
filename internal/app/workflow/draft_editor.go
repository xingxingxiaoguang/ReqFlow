package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

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
	next.Revision++
	if err := validateEditResult(next, e.catalog, ""); err != nil {
		return draft, err
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

func validateEditResult(draft domain.WorkflowDraft, catalog domain.CapabilityCatalog, insertedNodeID string) error {
	issues := domain.Validate(draft, catalog, domain.ValidateDraft)
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			return fmt.Errorf("编辑后流程非法 [%s] %s", issue.Code, issue.Message)
		}
		if insertedNodeID != "" && (issue.Code == "required_input_unconnected" || issue.Code == "required_rule_section_missing") &&
			(strings.Contains(issue.Path, insertedNodeID) || issue.Code == "required_rule_section_missing") {
			return fmt.Errorf("插入节点尚不可独立运行 [%s] %s", issue.Code, issue.Message)
		}
	}
	return nil
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
