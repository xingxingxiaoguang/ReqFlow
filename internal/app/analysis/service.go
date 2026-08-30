package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	maxAnalysisInstructionBytes = 128 << 10
	maxAnalysisNameBytes        = 200
)

type Options struct {
	Model         string
	MaxIterations int
}

type Service struct {
	repo      port.AnalysisRepo
	retrieval *appretrieval.Service
	llm       port.LLMClient
	model     string
	maxTurns  int
}

func NewService(repo port.AnalysisRepo, retrieval *appretrieval.Service, llm port.LLMClient,
	options Options) (*Service, error) {
	if repo == nil || retrieval == nil || llm == nil {
		return nil, fmt.Errorf("analysis service 依赖不完整")
	}
	if options.MaxIterations <= 0 {
		options.MaxIterations = 32
	}
	return &Service{repo: repo, retrieval: retrieval, llm: llm,
		model: strings.TrimSpace(options.Model), maxTurns: options.MaxIterations}, nil
}

type CreateProfileInput struct {
	WorkspaceID  string
	Name         string
	Instruction  string
	OutputSchema json.RawMessage
}

func (s *Service) CreateProfile(ctx context.Context, input CreateProfileInput) (*model.AnalysisProfile, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		input.WorkspaceID = "default"
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Instruction = strings.TrimSpace(input.Instruction)
	if input.Name == "" || len(input.Name) > maxAnalysisNameBytes {
		return nil, fmt.Errorf("AnalysisProfile 名称必须为 1..%d 字节", maxAnalysisNameBytes)
	}
	if input.Instruction == "" || len(input.Instruction) > maxAnalysisInstructionBytes {
		return nil, fmt.Errorf("instruction 必须为 1..%d 字节", maxAnalysisInstructionBytes)
	}
	normalizedSchema, schemaHash, err := logic.NormalizeDatasetSchema(input.OutputSchema)
	if err != nil {
		return nil, fmt.Errorf("output_schema 非法: %w", err)
	}
	hashPayload, _ := json.Marshal(map[string]any{
		"instruction": input.Instruction, "output_schema_hash": schemaHash,
	})
	digest := sha256.Sum256(hashPayload)
	profile := &model.AnalysisProfile{WorkspaceID: input.WorkspaceID, Name: input.Name,
		Instruction: input.Instruction, OutputSchema: normalizedSchema,
		ProfileHash: hex.EncodeToString(digest[:])}
	if err := s.repo.CreateAnalysisProfile(ctx, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func (s *Service) GetProfile(ctx context.Context, id string) (*model.AnalysisProfile, error) {
	return s.repo.GetAnalysisProfile(ctx, strings.TrimSpace(id))
}

func (s *Service) ListProfiles(ctx context.Context, workspaceID string, limit int) ([]model.AnalysisProfile, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	return s.repo.ListAnalysisProfiles(ctx, workspaceID, limit)
}

func (s *Service) CloneProfile(ctx context.Context, id, name string) (*model.AnalysisProfile, error) {
	source, err := s.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateProfile(ctx, CreateProfileInput{WorkspaceID: source.WorkspaceID,
		Name: name, Instruction: source.Instruction, OutputSchema: source.OutputSchema})
}

func (s *Service) GetResult(ctx context.Context, id string) (*model.AnalysisResult, error) {
	return s.repo.GetAnalysisResult(ctx, strings.TrimSpace(id))
}

type KnowledgeSourceInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type RunInput struct {
	WorkspaceID       string
	TaskID            string
	StepRunID         string
	ProducerAttempt   int
	AnalysisProfileID string
	Knowledge         map[string]KnowledgeSourceInput
	Inputs            map[string]model.ResourceRef
	Checkpoint        json.RawMessage
	SaveCheckpoint    func(json.RawMessage) error
	ReportProgress    func(map[string]any) error
}

func (s *Service) Analyze(ctx context.Context, input RunInput) (result *model.AnalysisResult, err error) {
	profile, err := s.GetProfile(ctx, input.AnalysisProfileID)
	if err != nil {
		return nil, fmt.Errorf("AnalysisProfile 不存在: %w", err)
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		input.WorkspaceID = profile.WorkspaceID
	}
	if profile.WorkspaceID != input.WorkspaceID {
		return nil, fmt.Errorf("AnalysisProfile 不属于任务 workspace")
	}
	result, err = s.repo.BeginAnalysisResult(ctx, &model.AnalysisResult{WorkspaceID: input.WorkspaceID,
		AnalysisProfileID: profile.ID, SourceTaskID: input.TaskID, SourceStepRunID: input.StepRunID},
		input.ProducerAttempt)
	if err != nil {
		return nil, err
	}
	if result.Status == model.AnalysisResultSucceeded {
		return result, nil
	}
	defer func() {
		if err != nil {
			_ = s.repo.FailAnalysisResult(context.WithoutCancel(ctx), result.ID, input.StepRunID,
				input.ProducerAttempt, truncate(err.Error(), 4000))
		}
	}()

	scopeSources := make(map[string]appretrieval.KnowledgeSource, len(input.Knowledge))
	for portName, sourceInput := range input.Knowledge {
		resource, ok := input.Inputs[portName]
		if !ok || resource.ResourceType != model.ResourceRetrievalSnapshot {
			return nil, fmt.Errorf("知识源端口 %s 必须绑定 RetrievalSnapshot", portName)
		}
		name := strings.TrimSpace(sourceInput.Name)
		if name == "" {
			name = portName
		}
		scopeSources[name] = appretrieval.KnowledgeSource{Name: name,
			Description: strings.TrimSpace(sourceInput.Description), SnapshotID: resource.ResourceID}
	}
	tools, err := s.retrieval.BuildKnowledgeTools(ctx, appretrieval.KnowledgeScope{
		ID: input.StepRunID, WorkspaceID: input.WorkspaceID, Sources: scopeSources,
	})
	if err != nil {
		return nil, err
	}

	agentContext, err := analysisContext(input.Checkpoint, profile, input.Knowledge, tools)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var callbackErr error
	saveContext := func() {
		if callbackErr != nil || input.SaveCheckpoint == nil {
			return
		}
		raw, marshalErr := json.Marshal(agentContext)
		if marshalErr == nil {
			marshalErr = input.SaveCheckpoint(raw)
		}
		if marshalErr != nil {
			callbackErr = marshalErr
			cancel()
		}
	}
	report := func(payload map[string]any) {
		if callbackErr != nil || input.ReportProgress == nil {
			return
		}
		if reportErr := input.ReportProgress(payload); reportErr != nil {
			callbackErr = reportErr
			cancel()
		}
	}
	report(map[string]any{"phase": "analyzing", "analysis_result_id": result.ID})
	loop := agent.New(s.llm, tools, agent.Config{MaxIterations: s.maxTurns})
	finalContext, runErr := loop.Run(runCtx, agentContext, nil, func(event agent.Event) {
		switch event.Type {
		case "message_end":
			saveContext()
		case "tool_execution_end":
			report(map[string]any{"phase": "tool", "tool": event.ToolName,
				"analysis_result_id": result.ID, "is_error": event.Output.IsError})
		}
	})
	if callbackErr != nil {
		return nil, callbackErr
	}
	if runErr != nil {
		return nil, runErr
	}
	output, err := finalStructuredOutput(finalContext, profile.OutputSchema)
	if err != nil {
		return nil, err
	}
	contextJSON, _ := json.Marshal(finalContext)
	usage := contextUsage(finalContext)
	result.Status, result.Output, result.AgentContext, result.Model =
		model.AnalysisResultSucceeded, output, contextJSON, s.model
	result.InputTokens, result.OutputTokens = usage.Input, usage.Output
	result.CacheReadTokens, result.CacheWriteTokens = usage.CacheRead, usage.CacheWrite
	if err = s.repo.CompleteAnalysisResult(ctx, result, input.ProducerAttempt); err != nil {
		return nil, err
	}
	result.FinishedAt = time.Now()
	report(map[string]any{"phase": "succeeded", "analysis_result_id": result.ID,
		"input_tokens": result.InputTokens, "output_tokens": result.OutputTokens})
	return result, nil
}

func analysisContext(checkpoint json.RawMessage, profile *model.AnalysisProfile,
	sources map[string]KnowledgeSourceInput, tools []agent.Tool) (*port.Context, error) {
	if len(strings.TrimSpace(string(checkpoint))) > 0 && string(checkpoint) != "{}" {
		var restored port.Context
		if err := json.Unmarshal(checkpoint, &restored); err != nil {
			return nil, fmt.Errorf("analysis checkpoint 非法: %w", err)
		}
		if strings.TrimSpace(restored.SystemPrompt) == "" || len(restored.Messages) == 0 {
			return nil, fmt.Errorf("analysis checkpoint 缺少会话")
		}
		return &restored, nil
	}
	toolGuides := make([]string, 0, len(tools))
	for _, tool := range tools {
		if documented, ok := tool.(agent.DocumentedTool); ok {
			toolGuides = append(toolGuides, documented.PromptSnippet())
		}
	}
	schema := string(profile.OutputSchema)
	system := "你是 ReqFlow V2 的结构化分析 Agent。只能使用已授权知识工具获取事实，不得猜测资源 ID。" +
		"所有结论必须在输出中保留可验证的 dataset_item_id/provenance 引用。\n\n业务指令：\n" +
		profile.Instruction + "\n\n可用工具：\n- " + strings.Join(toolGuides, "\n- ") +
		"\n\n最终回复必须只包含一个 JSON object，不得使用 Markdown 代码围栏或附加解释，并严格符合此 JSON Schema：\n" + schema
	manifest, _ := json.Marshal(map[string]any{"knowledge_sources": sources, "output_schema": json.RawMessage(schema)})
	return &port.Context{SystemPrompt: system,
		Messages:   []port.Message{port.NewUserMessage("请执行配置的分析任务。运行合同：" + string(manifest))},
		TaskSchema: schema}, nil
}

func finalStructuredOutput(ctx *port.Context, schema json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		return nil, fmt.Errorf("Agent 未返回上下文")
	}
	text := ""
	for i := len(ctx.Messages) - 1; i >= 0; i-- {
		if ctx.Messages[i].Role == port.RoleAssistant && len(ctx.Messages[i].ToolCalls()) == 0 {
			text = strings.TrimSpace(ctx.Messages[i].Text())
			break
		}
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("Agent 最终输出不是 JSON object")
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(text[start : end+1]))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("解析 Agent 最终 JSON: %w", err)
	}
	raw, _ := json.Marshal(object)
	normalized, err := logic.NormalizeDatasetItem(schema, raw)
	if err != nil {
		return nil, fmt.Errorf("Agent 输出不符合 AnalysisProfile Schema: %w", err)
	}
	return normalized, nil
}

func contextUsage(ctx *port.Context) port.Usage {
	var total port.Usage
	if ctx == nil {
		return total
	}
	for _, message := range ctx.Messages {
		total.Input += message.Usage.Input
		total.Output += message.Usage.Output
		total.CacheRead += message.Usage.CacheRead
		total.CacheWrite += message.Usage.CacheWrite
	}
	return total
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
