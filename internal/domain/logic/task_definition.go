package logic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"reqflow/internal/domain/model"
)

const (
	MaxTaskDefinitionSteps = 64
	MaxTaskDefinitionPorts = 32
)

var validV2StepKinds = map[model.StepKind]bool{
	model.StepKindSourceParse: true, model.StepKindDocumentExtract: true,
	model.StepKindDataTransform: true, model.StepKindDataValidate: true,
	model.StepKindDataPublish: true, model.StepKindDataQueryDerive: true,
	model.StepKindAnalysisPublish:  true,
	model.StepKindRetrievalBuild:   true,
	model.StepKindKnowledgeAnalyze: true, model.StepKindArtifactRender: true,
	model.StepKindGraphBuild: true, model.StepKindHumanReview: true,
}

var validResourceTypes = map[model.ResourceType]bool{
	model.ResourceAssetSet: true, model.ResourceParsedDocuments: true,
	model.ResourceRecordDrafts: true, model.ResourceTransformedRecords: true,
	model.ResourceValidationResults: true, model.ResourceApprovedRecords: true,
	model.ResourceDataset: true, model.ResourceDatasetBoundary: true,
	model.ResourceDatasetBatch: true, model.ResourcePipelineCursor: true,
	model.ResourceRetrievalSnapshot: true,
	model.ResourceAnalysisResult:    true,
	model.ResourceArtifact:          true,
}

// ValidateTaskDefinition 校验 V2 工作流的 DAG、端口引用和资源类型。
func ValidateTaskDefinition(def model.TaskDefinition) error {
	if !IsValidIdentifier(def.Key) {
		return fmt.Errorf("任务定义 key 非法，必须为 snake_case")
	}
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("任务定义名称不能为空")
	}
	if len(def.Steps) == 0 || len(def.Steps) > MaxTaskDefinitionSteps {
		return fmt.Errorf("任务定义步骤数必须在 1..%d 之间", MaxTaskDefinitionSteps)
	}
	if err := validatePorts("输入", def.InputPorts, MaxTaskDefinitionPorts); err != nil {
		return err
	}
	if err := validatePorts("输出", def.OutputPorts, MaxTaskDefinitionPorts); err != nil {
		return err
	}

	steps := make(map[string]model.StepDefinition, len(def.Steps))
	for _, step := range def.Steps {
		if !IsValidIdentifier(step.ID) {
			return fmt.Errorf("步骤 ID %q 非法，必须为 snake_case", step.ID)
		}
		if _, exists := steps[step.ID]; exists {
			return fmt.Errorf("步骤 ID 重复: %s", step.ID)
		}
		if strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("步骤 %s 名称不能为空", step.ID)
		}
		if !validV2StepKinds[step.Kind] {
			return fmt.Errorf("步骤 %s 的执行器类型非法: %s", step.ID, step.Kind)
		}
		if len(step.Config) > 0 && !json.Valid(step.Config) {
			return fmt.Errorf("步骤 %s 的 config 不是合法 JSON", step.ID)
		}
		for port, typ := range step.Outputs {
			if !IsValidIdentifier(port) {
				return fmt.Errorf("步骤 %s 的输出端口 %q 非法", step.ID, port)
			}
			if !validResourceTypes[typ] {
				return fmt.Errorf("步骤 %s 的输出端口 %s 资源类型非法: %s", step.ID, port, typ)
			}
		}
		steps[step.ID] = step
	}

	for _, step := range def.Steps {
		seen := map[string]bool{}
		for _, dep := range step.DependsOn {
			if dep == step.ID {
				return fmt.Errorf("步骤 %s 不能依赖自身", step.ID)
			}
			if _, exists := steps[dep]; !exists {
				return fmt.Errorf("步骤 %s 依赖不存在的步骤 %s", step.ID, dep)
			}
			if seen[dep] {
				return fmt.Errorf("步骤 %s 重复依赖 %s", step.ID, dep)
			}
			seen[dep] = true
		}
	}
	if _, err := TaskDefinitionOrder(def); err != nil {
		return err
	}

	for _, step := range def.Steps {
		ancestors := dependencyAncestors(step.ID, steps)
		for inputPort, ref := range step.Inputs {
			if !IsValidIdentifier(inputPort) {
				return fmt.Errorf("步骤 %s 的输入端口 %q 非法", step.ID, inputPort)
			}
			if _, err := resolveResourceRef(ref, def, steps, ancestors); err != nil {
				return fmt.Errorf("步骤 %s 输入 %s: %w", step.ID, inputPort, err)
			}
		}
	}

	if len(def.OutputPorts) != len(def.OutputBindings) {
		return fmt.Errorf("每个任务输出端口都必须且只能绑定一个步骤输出")
	}
	allSteps := make(map[string]bool, len(steps))
	for id := range steps {
		allSteps[id] = true
	}
	for port, output := range def.OutputPorts {
		ref, exists := def.OutputBindings[port]
		if !exists {
			return fmt.Errorf("任务输出端口 %s 缺少绑定", port)
		}
		typ, err := resolveResourceRef(ref, def, steps, allSteps)
		if err != nil {
			return fmt.Errorf("任务输出端口 %s: %w", port, err)
		}
		if typ != output.ResourceType {
			return fmt.Errorf("任务输出端口 %s 类型为 %s，但绑定资源类型为 %s", port, output.ResourceType, typ)
		}
	}
	for port := range def.OutputBindings {
		if _, exists := def.OutputPorts[port]; !exists {
			return fmt.Errorf("存在未声明的任务输出绑定 %s", port)
		}
	}
	return nil
}

// NormalizeTaskDefinition 清除持久化元数据，返回稳定定义快照和哈希。
func NormalizeTaskDefinition(def model.TaskDefinition) (json.RawMessage, string, error) {
	if err := ValidateTaskDefinition(def); err != nil {
		return nil, "", err
	}
	def.ID = ""
	def.WorkspaceID = ""
	def.Status = ""
	def.DefinitionHash = ""
	def.CreatedAt = time.Time{}
	def.UpdatedAt = time.Time{}
	canonical, err := json.Marshal(def)
	if err != nil {
		return nil, "", fmt.Errorf("序列化任务定义: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

// TaskDefinitionOrder 返回稳定拓扑顺序；同层步骤按定义中的原始顺序排列。
func TaskDefinitionOrder(def model.TaskDefinition) ([]string, error) {
	position := map[string]int{}
	indegree := map[string]int{}
	children := map[string][]string{}
	for i, step := range def.Steps {
		position[step.ID] = i
		indegree[step.ID] = len(step.DependsOn)
		for _, dep := range step.DependsOn {
			children[dep] = append(children[dep], step.ID)
		}
	}
	ready := make([]string, 0)
	for _, step := range def.Steps {
		if indegree[step.ID] == 0 {
			ready = append(ready, step.ID)
		}
	}
	order := make([]string, 0, len(def.Steps))
	for len(ready) > 0 {
		sort.SliceStable(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}
	if len(order) != len(def.Steps) {
		return nil, fmt.Errorf("任务定义步骤依赖存在环")
	}
	return order, nil
}

func validatePorts(label string, ports map[string]model.PortDefinition, limit int) error {
	if len(ports) > limit {
		return fmt.Errorf("任务%s端口超过 %d 个", label, limit)
	}
	for name, port := range ports {
		if !IsValidIdentifier(name) {
			return fmt.Errorf("任务%s端口 %q 非法", label, name)
		}
		if !validResourceTypes[port.ResourceType] {
			return fmt.Errorf("任务%s端口 %s 的资源类型非法: %s", label, name, port.ResourceType)
		}
	}
	return nil
}

func resolveResourceRef(ref string, def model.TaskDefinition, steps map[string]model.StepDefinition, allowedSteps map[string]bool) (model.ResourceType, error) {
	if strings.HasPrefix(ref, "$task.") {
		port := strings.TrimPrefix(ref, "$task.")
		if strings.Contains(port, ".") || !IsValidIdentifier(port) {
			return "", fmt.Errorf("任务资源引用格式非法: %s", ref)
		}
		decl, exists := def.InputPorts[port]
		if !exists {
			return "", fmt.Errorf("引用了不存在的任务输入端口 %s", port)
		}
		return decl.ResourceType, nil
	}
	if strings.HasPrefix(ref, "$step.") {
		parts := strings.Split(strings.TrimPrefix(ref, "$step."), ".")
		if len(parts) != 2 || !IsValidIdentifier(parts[0]) || !IsValidIdentifier(parts[1]) {
			return "", fmt.Errorf("步骤资源引用格式非法: %s", ref)
		}
		if !allowedSteps[parts[0]] {
			return "", fmt.Errorf("只能引用当前步骤的依赖祖先输出: %s", ref)
		}
		step, exists := steps[parts[0]]
		if !exists {
			return "", fmt.Errorf("引用了不存在的步骤 %s", parts[0])
		}
		typ, exists := step.Outputs[parts[1]]
		if !exists {
			return "", fmt.Errorf("步骤 %s 不存在输出端口 %s", parts[0], parts[1])
		}
		return typ, nil
	}
	return "", fmt.Errorf("资源引用必须以 $task. 或 $step. 开头")
}

func dependencyAncestors(stepID string, steps map[string]model.StepDefinition) map[string]bool {
	out := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		for _, dep := range steps[id].DependsOn {
			if out[dep] {
				continue
			}
			out[dep] = true
			visit(dep)
		}
	}
	visit(stepID)
	return out
}
