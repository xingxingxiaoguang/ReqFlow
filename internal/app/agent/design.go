package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type ProposalSink interface {
	AddProposal(domain.CommandProposal, time.Time) error
}

type ProposalTool struct {
	Sink ProposalSink
	Now  func() time.Time
}

func (t *ProposalTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "submit_command_proposals", Description: "提交待用户接受的 Workflow Draft 命令建议；不会直接修改 Draft。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"proposals":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"draft_revision":{"type":"integer"},"summary":{"type":"string"},"command":{"type":"object"}},"required":["id","draft_revision","summary","command"],"additionalProperties":false}}},"required":["proposals"],"additionalProperties":false}`)}
}

func (t *ProposalTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) ToolOutput {
	if t == nil || t.Sink == nil {
		return ToolOutput{Output: "Proposal sink 未配置", IsError: true}
	}
	var payload struct {
		Proposals []struct {
			ID            string          `json:"id"`
			DraftRevision int64           `json:"draft_revision"`
			Summary       string          `json:"summary"`
			Command       json.RawMessage `json:"command"`
		} `json:"proposals"`
	}
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || len(payload.Proposals) == 0 {
		return ToolOutput{Output: "proposals 必须是非空数组且字段合法", IsError: true}
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	for _, proposal := range payload.Proposals {
		if err := t.Sink.AddProposal(domain.CommandProposal{ID: proposal.ID, DraftRevision: proposal.DraftRevision,
			Summary: proposal.Summary, Command: proposal.Command}, now); err != nil {
			return ToolOutput{Output: "提交命令建议失败: " + err.Error(), IsError: true}
		}
	}
	return ToolOutput{Output: fmt.Sprintf(`{"submitted":true,"count":%d}`, len(payload.Proposals)),
		Details: "命令建议已记录，等待用户接受后才会执行", Status: domain.ToolCompleted, Terminate: true}
}

type HumanQuestionTool struct {
	Session *domain.DesignSession
	Now     func() time.Time
}

func (t *HumanQuestionTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "request_human_decision", Description: "当业务判断无法安全自动化时，暂停 Agent 并请求用户决策。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"path":{"type":"string"},"prompt":{"type":"string"},"context":{"type":"object"}},"required":["id","path","prompt"],"additionalProperties":false}`)}
}

func (t *HumanQuestionTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) ToolOutput {
	if t == nil || t.Session == nil {
		return ToolOutput{Output: "DesignSession 未配置", IsError: true}
	}
	var question domain.HumanQuestion
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&question); err != nil {
		return ToolOutput{Output: "人工问题参数非法: " + err.Error(), IsError: true}
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	if err := t.Session.RequestHuman(question, now); err != nil {
		return ToolOutput{Output: "请求人工决策失败: " + err.Error(), IsError: true}
	}
	return ToolOutput{Output: `{"status":"needs_human"}`, Details: question.Prompt,
		Status: domain.ToolNeedsHuman, Question: t.Session.PendingQuestion}
}

type DesignAgent struct {
	LLM     port.LLMClient
	Catalog domain.CapabilityCatalog
}

func (a *DesignAgent) Run(ctx context.Context, session *domain.DesignSession, state *RunState,
	tools []Tool, request string, options RunOptions) error {
	if a == nil || a.LLM == nil || a.Catalog == nil || session == nil || state == nil {
		return fmt.Errorf("design agent 依赖不完整")
	}
	if session.Mode != domain.AssistanceAgent || session.Status != domain.DesignAgentRunning {
		return fmt.Errorf("DesignSession 当前不可运行 Agent")
	}
	if strings.TrimSpace(request) == "" {
		return fmt.Errorf("设计目标不能为空")
	}
	state.Context.SystemPrompt = BuildDesignPrompt(a.Catalog, tools)
	state.Context.Messages = append(state.Context.Messages, port.NewUserMessage(request))
	return Execute(ctx, a.LLM, tools, state, options.TraceEnvelope, options)
}

func BuildDesignPrompt(catalog domain.CapabilityCatalog, tools []Tool) string {
	var builder strings.Builder
	builder.WriteString("你是 ReqFlow 线性工作流设计助手。只能提出 Draft Command Proposal，不能直接修改草稿、发布 Revision 或调用仓储。\n")
	builder.WriteString("节点只能形成单链；所有自动判断必须携带证据、置信度和理由；记录粒度、唯一键和硬约束必须请求人工确认。\n\n")
	builder.WriteString("可用 Capability：\n")
	if definitions, ok := catalog.(interface {
		Definitions() []domain.CapabilityDefinition
	}); ok {
		for _, definition := range definitions.Definitions() {
			builder.WriteString(fmt.Sprintf("- %s@%d：%s；LLM=%t，人工完成=%t\n", definition.Ref.Kind, definition.Ref.Version,
				definition.Label, definition.RequiresLLM, definition.ManualCompletion))
		}
	}
	builder.WriteString("\n本次实际可用工具：\n")
	for _, tool := range tools {
		builder.WriteString("- " + tool.Spec().Name)
		if documented, ok := tool.(DocumentedTool); ok && strings.TrimSpace(documented.PromptSnippet()) != "" {
			builder.WriteString("：" + strings.TrimSpace(documented.PromptSnippet()))
		}
		builder.WriteString("\n")
	}
	builder.WriteString("最终必须调用 submit_command_proposals 或 request_human_decision；不要把自然语言回复当作完成。")
	return builder.String()
}

func CloneRunState(state RunState) (RunState, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return RunState{}, err
	}
	var clone RunState
	if err := json.Unmarshal(raw, &clone); err != nil {
		return RunState{}, err
	}
	return clone, nil
}
