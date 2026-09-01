package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type ValidationError struct {
	Issues []ValidationIssue
}

func (e ValidationError) Error() string {
	for _, issue := range e.Issues {
		if issue.Severity == SeverityError {
			return fmt.Sprintf("工作流校验失败 [%s] %s", issue.Code, issue.Message)
		}
	}
	return "工作流校验失败"
}

func LinearOrder(draft WorkflowDraft) ([]string, error) {
	if len(draft.Nodes) == 0 {
		return nil, fmt.Errorf("线性流程至少需要一个节点")
	}
	nodes := make(map[string]bool, len(draft.Nodes))
	next := make(map[string]string, len(draft.Nodes)-1)
	parent := make(map[string]string, len(draft.Nodes)-1)
	for _, node := range draft.Nodes {
		if nodes[node.ID] {
			return nil, fmt.Errorf("节点 ID 重复: %s", node.ID)
		}
		nodes[node.ID] = true
	}
	for _, connection := range draft.Connections {
		if connection.From.Kind != EndpointNodeOutput || connection.To.Kind != EndpointNodeInput {
			continue
		}
		from, to := connection.From.NodeID, connection.To.NodeID
		if !nodes[from] || !nodes[to] {
			return nil, fmt.Errorf("主连接引用了不存在的节点")
		}
		if _, exists := next[from]; exists {
			return nil, fmt.Errorf("节点 %s 存在多个后继", from)
		}
		if _, exists := parent[to]; exists {
			return nil, fmt.Errorf("节点 %s 存在多个前驱", to)
		}
		next[from], parent[to] = to, from
	}
	if len(next) != len(nodes)-1 {
		return nil, fmt.Errorf("节点主连接没有形成完整单链")
	}
	head := ""
	for id := range nodes {
		if _, exists := parent[id]; !exists {
			if head != "" {
				return nil, fmt.Errorf("线性流程存在多个起始节点")
			}
			head = id
		}
	}
	if head == "" {
		return nil, fmt.Errorf("线性流程不存在起始节点，可能存在循环")
	}
	order := make([]string, 0, len(nodes))
	visited := map[string]bool{}
	for current := head; current != ""; current = next[current] {
		if visited[current] {
			return nil, fmt.Errorf("线性流程存在循环")
		}
		visited[current] = true
		order = append(order, current)
	}
	if len(order) != len(nodes) {
		return nil, fmt.Errorf("节点主连接没有覆盖全部节点")
	}
	return order, nil
}

func BuildRevision(draft WorkflowDraft, catalog CapabilityCatalog, revisionID, publishedBy string,
	publishedAt time.Time) (*WorkflowRevision, error) {
	issues := Validate(draft, catalog, ValidatePublish)
	if HasErrors(issues) {
		return nil, ValidationError{Issues: issues}
	}
	if strings.TrimSpace(revisionID) == "" || strings.TrimSpace(publishedBy) == "" || publishedAt.IsZero() {
		return nil, fmt.Errorf("revision_id、published_by 和 published_at 必须有效")
	}
	order, err := LinearOrder(draft)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]WorkflowNode, len(draft.Nodes))
	for _, node := range draft.Nodes {
		byID[node.ID] = node
	}
	resolved := make([]ResolvedNode, 0, len(order))
	for _, id := range order {
		node := byID[id]
		capability, exists := catalog.Lookup(node.Capability)
		if !exists {
			return nil, fmt.Errorf("capability %s 在发布时消失", capabilityKey(node.Capability))
		}
		config := node.Config
		if len(config) == 0 {
			config = capability.DefaultConfig
		}
		resolved = append(resolved, ResolvedNode{ID: node.ID, Name: node.Name,
			Capability: capability, Config: append(json.RawMessage(nil), config...)})
	}

	connections := append([]Connection(nil), draft.Connections...)
	sort.Slice(connections, func(i, j int) bool {
		left := endpointKey(connections[i].From) + "->" + endpointKey(connections[i].To)
		right := endpointKey(connections[j].From) + "->" + endpointKey(connections[j].To)
		return left < right
	})
	testCases := append([]AcceptanceCase(nil), draft.AcceptanceCases...)
	sort.Slice(testCases, func(i, j int) bool { return testCases[i].ID < testCases[j].ID })

	rules, err := cloneRuleBundle(draft.Rules)
	if err != nil {
		return nil, err
	}
	revision := &WorkflowRevision{
		ID: revisionID, WorkflowID: draft.ID, WorkspaceID: draft.WorkspaceID,
		Key: draft.Key, Name: strings.TrimSpace(draft.Name), Description: strings.TrimSpace(draft.Description),
		Inputs: append([]WorkflowPort(nil), draft.Inputs...), Outputs: append([]WorkflowPort(nil), draft.Outputs...),
		Nodes: resolved, Connections: connections, Rules: rules,
		AcceptanceCases: testCases, PublishedBy: strings.TrimSpace(publishedBy), PublishedAt: publishedAt,
	}
	content := struct {
		WorkflowID      string           `json:"workflow_id"`
		WorkspaceID     string           `json:"workspace_id"`
		Key             string           `json:"key"`
		Name            string           `json:"name"`
		Description     string           `json:"description,omitempty"`
		Inputs          []WorkflowPort   `json:"inputs"`
		Outputs         []WorkflowPort   `json:"outputs"`
		Nodes           []ResolvedNode   `json:"nodes"`
		Connections     []Connection     `json:"connections"`
		Rules           RuleBundle       `json:"rules"`
		AcceptanceCases []AcceptanceCase `json:"acceptance_cases,omitempty"`
	}{
		WorkflowID: revision.WorkflowID, WorkspaceID: revision.WorkspaceID, Key: revision.Key,
		Name: revision.Name, Description: revision.Description, Inputs: revision.Inputs, Outputs: revision.Outputs,
		Nodes: revision.Nodes, Connections: revision.Connections, Rules: revision.Rules,
		AcceptanceCases: revision.AcceptanceCases,
	}
	canonical, err := json.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("序列化 Workflow Revision: %w", err)
	}
	sum := sha256.Sum256(canonical)
	revision.ContentHash = hex.EncodeToString(sum[:])
	return revision, nil
}

func cloneRuleBundle(bundle RuleBundle) (RuleBundle, error) {
	raw, err := json.Marshal(bundle)
	if err != nil {
		return RuleBundle{}, fmt.Errorf("复制 RuleBundle: %w", err)
	}
	var cloned RuleBundle
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return RuleBundle{}, fmt.Errorf("复制 RuleBundle: %w", err)
	}
	return cloned, nil
}
