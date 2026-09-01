package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

const (
	maxAnalysisInstructionBytes = 128 << 10
)

type Options struct {
	Model          string
	MaxIterations  int
	ConfigResolver port.PlatformConfigResolver
}

type Service struct {
	repo      port.AnalysisRepo
	retrieval *appretrieval.Service
	llm       port.LLMClient
	model     string
	resolver  port.PlatformConfigResolver
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
		model: strings.TrimSpace(options.Model), resolver: options.ConfigResolver,
		maxTurns: options.MaxIterations}, nil
}

func (s *Service) GetResult(ctx context.Context, id string) (*model.AnalysisResult, error) {
	return s.repo.GetAnalysisResult(ctx, strings.TrimSpace(id))
}

type KnowledgeSourceInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type RunInput struct {
	WorkspaceID     string
	WorkflowRunID   string
	NodeRunID       string
	ProducerAttempt int
	Instruction     string
	OutputContract  domain.OutputContract
	Knowledge       map[string]KnowledgeSourceInput
	Inputs          map[string]domain.NodeResourceBinding
	Checkpoint      json.RawMessage
	SaveCheckpoint  func(json.RawMessage) error
	ReportProgress  func(map[string]any) error
}

type analysisCheckpoint struct {
	agent.TraceEnvelope
	AnalysisResultID string         `json:"analysis_result_id"`
	Run              agent.RunState `json:"run"`
	Result           resultState    `json:"result"`
}

func (s *Service) Analyze(ctx context.Context, input RunInput) (result *model.AnalysisResult, err error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		input.WorkspaceID = "default"
	}
	input.Instruction = strings.TrimSpace(input.Instruction)
	if input.Instruction == "" || len(input.Instruction) > maxAnalysisInstructionBytes {
		return nil, fmt.Errorf("analysis instruction 必须为 1..%d 字节", maxAnalysisInstructionBytes)
	}
	outputSchema, outputSchemaHash, err := domain.CompileOutputContract(input.OutputContract)
	if err != nil {
		return nil, fmt.Errorf("编译 OutputContract: %w", err)
	}
	outputContractRaw, err := json.Marshal(input.OutputContract)
	if err != nil {
		return nil, err
	}
	outputContractHash, err := domain.HashContract(input.OutputContract)
	if err != nil {
		return nil, err
	}
	result, err = s.repo.BeginAnalysisResult(ctx, &model.AnalysisResult{WorkspaceID: input.WorkspaceID,
		Instruction: input.Instruction, OutputContract: outputContractRaw, OutputContractHash: outputContractHash,
		OutputSchema: outputSchema, OutputSchemaHash: outputSchemaHash,
		ProducerWorkflowRunID: input.WorkflowRunID, ProducerNodeRunID: input.NodeRunID},
		input.ProducerAttempt)
	if err != nil {
		return nil, err
	}
	if result.Status == model.AnalysisResultSucceeded {
		return result, nil
	}
	resultID := result.ID
	defer func() {
		if err != nil {
			_ = s.repo.FailAnalysisResult(context.WithoutCancel(ctx), resultID, input.NodeRunID,
				input.ProducerAttempt, truncate(err.Error(), 4000))
		}
	}()
	modelName, err := s.resolveModel(ctx)
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeAnalysisCheckpoint(input.Checkpoint)
	if err != nil {
		return nil, err
	}
	if checkpoint.AnalysisResultID != "" && checkpoint.AnalysisResultID != result.ID {
		return nil, fmt.Errorf("knowledge.analyze checkpoint 属于不同 AnalysisResult")
	}
	checkpoint.AnalysisResultID = result.ID

	if !checkpoint.Result.Submitted {
		scopeSources := make(map[string]appretrieval.KnowledgeSource, len(input.Knowledge))
		for portName, sourceInput := range input.Knowledge {
			resource, ok := input.Inputs[portName]
			if !ok || resource.ResourceType != domain.ResourceRetrievalSnapshot {
				return nil, fmt.Errorf("知识源端口 %s 必须绑定 RetrievalSnapshot", portName)
			}
			name := strings.TrimSpace(sourceInput.Name)
			if name == "" {
				name = portName
			}
			scopeSources[name] = appretrieval.KnowledgeSource{Name: name,
				Description: strings.TrimSpace(sourceInput.Description), SnapshotID: resource.ResourceID}
		}
		tools, buildErr := s.retrieval.BuildKnowledgeTools(ctx, appretrieval.KnowledgeScope{
			ID: input.NodeRunID, WorkspaceID: input.WorkspaceID, Sources: scopeSources,
		})
		if buildErr != nil {
			return nil, buildErr
		}
		tools = append(tools, &submitResultTool{schema: result.OutputSchema, state: &checkpoint.Result})
		if strings.TrimSpace(checkpoint.Run.Context.SystemPrompt) == "" {
			checkpoint.Run.Context = analysisContext(result.Instruction, result.OutputSchema, input.Knowledge, tools)
		}
		runErr := agent.Execute(ctx, s.llm, tools, &checkpoint.Run, &checkpoint.TraceEnvelope, agent.RunOptions{
			ID: input.NodeRunID, Label: "知识分析", Ordinal: 0,
			Loop: agent.Config{MaxIterations: s.maxTurns, RequireToolTermination: true,
				NoToolCallReminder: "分析尚未提交。请继续使用知识工具核对事实，并通过 submit_analysis_result 提交符合 Schema 的最终结果。"},
			Stats: func() map[string]int {
				if checkpoint.Result.Submitted {
					return map[string]int{"submitted": 1}
				}
				return nil
			},
			OnFlush: func(trace agent.RunTrace) error {
				if input.SaveCheckpoint != nil {
					raw, marshalErr := json.Marshal(&checkpoint)
					if marshalErr != nil {
						return marshalErr
					}
					if saveErr := input.SaveCheckpoint(raw); saveErr != nil {
						return saveErr
					}
				}
				if input.ReportProgress != nil {
					return input.ReportProgress(map[string]any{"phase": "agent", "status": trace.Status,
						"agent_run_id": trace.ID, "analysis_result_id": result.ID,
						"request_count": trace.RequestCount, "tool_count": len(trace.Tools)})
				}
				return nil
			},
		})
		if runErr != nil {
			return nil, runErr
		}
	}
	if !checkpoint.Result.Submitted || len(checkpoint.Result.Output) == 0 {
		return nil, fmt.Errorf("分析 Agent 未调用 submit_analysis_result")
	}
	contextJSON, _ := json.Marshal(&checkpoint.Run.Context)
	usage := agent.ContextUsage(&checkpoint.Run.Context)
	result.Status, result.Output, result.AgentContext, result.Model =
		model.AnalysisResultSucceeded, checkpoint.Result.Output, contextJSON, modelName
	result.InputTokens, result.OutputTokens = usage.InputTokens, usage.OutputTokens
	result.CacheReadTokens, result.CacheWriteTokens = usage.CacheReadTokens, usage.CacheWriteTokens
	if err = s.repo.CompleteAnalysisResult(ctx, result, input.ProducerAttempt); err != nil {
		return nil, err
	}
	result.FinishedAt = time.Now()
	return result, nil
}

func (s *Service) resolveModel(ctx context.Context) (string, error) {
	if s.resolver != nil {
		config, err := s.resolver.ResolveLLM(ctx)
		if err != nil {
			return "", fmt.Errorf("读取当前 LLM 配置: %w", err)
		}
		if modelName := strings.TrimSpace(config.Model); modelName != "" {
			return modelName, nil
		}
	}
	if modelName := strings.TrimSpace(s.model); modelName != "" {
		return modelName, nil
	}
	return "", fmt.Errorf("当前 LLM 配置缺少 model")
}

func analysisContext(instruction string, outputSchema json.RawMessage,
	sources map[string]KnowledgeSourceInput, tools []agent.Tool) port.Context {
	toolGuides := make([]string, 0, len(tools))
	var guidelines []string
	for _, tool := range tools {
		if documented, ok := tool.(agent.DocumentedTool); ok {
			toolGuides = append(toolGuides, documented.PromptSnippet())
			guidelines = append(guidelines, documented.PromptGuidelines()...)
		}
	}
	schema := string(outputSchema)
	system := "你是 ReqFlow 的结构化分析 Agent。只能使用已授权知识工具获取事实，不得猜测资源 ID。" +
		"所有结论必须在输出中保留可验证的 dataset_item_id/provenance 引用。\n\n业务指令：\n" +
		instruction + "\n\n可用工具：\n- " + strings.Join(toolGuides, "\n- ") +
		"\n\n工具规则：\n- " + strings.Join(guidelines, "\n- ") +
		"\n\n不得用普通文本作为最终结果；submit_analysis_result 是唯一完成出口，其参数必须严格符合此 JSON Schema：\n" + schema
	manifest, _ := json.Marshal(map[string]any{"knowledge_sources": sources, "output_schema": json.RawMessage(schema)})
	return port.Context{SystemPrompt: system,
		Messages:     []port.Message{port.NewUserMessage("请执行配置的分析任务。运行合同：" + string(manifest))},
		OutputSchema: schema}
}

func decodeAnalysisCheckpoint(raw json.RawMessage) (analysisCheckpoint, error) {
	var checkpoint analysisCheckpoint
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return checkpoint, nil
	}
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return checkpoint, fmt.Errorf("knowledge.analyze checkpoint 非法: %w", err)
	}
	if strings.TrimSpace(checkpoint.Run.Context.SystemPrompt) == "" || len(checkpoint.Run.Context.Messages) == 0 {
		return checkpoint, fmt.Errorf("knowledge.analyze checkpoint 缺少 Agent 会话")
	}
	return checkpoint, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
