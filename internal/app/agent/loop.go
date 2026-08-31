// Package agent 实现 pi 式极简 agent loop，移植自
// https://github.com/earendil-works/pi（MIT License, Copyright (c) 2025 Mario Zechner）
// 的 packages/agent/src/agent-loop.ts 子集。
//
// 终止条件与 pi 一致：模型回复不含工具调用即结束（自然终止）。
// 有意偏离 pi：
//   - 回调事件替代 async iterator（Go 惯例；中止走 ctx）
//   - MaxIterations 安全阀（默认 8）——pi 刻意不设上限，但生产导入场景必须兜底
//   - 仅顺序执行工具（pi 的 parallel 模式后置）
//   - 不移植：steering/followUp 消息队列、beforeToolCall/afterToolCall 钩子、
//     prepareNextTurn 动态换模型——"if I don't need it, it won't be built"
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"reqflow/internal/port"
)

// ToolOutput 工具执行结果。Output 给 LLM（回传上下文）；Details 仅给 UI 展示
// （pi 的 output/details 拆分——UI 需要的信息与模型需要的信息不同）。
// Terminate=true 表示请求 loop 就此收束（pi 的 terminate 语义）。
type ToolOutput struct {
	Output    string
	Details   string
	IsError   bool
	Terminate bool
}

// Tool 工具接口。Spec 返回的 Parameters 为 JSON Schema；Execute 收到原始 JSON 参数。
// onProgress 可选地推送中间进度（UI 展示用，不进 LLM 上下文）。
type Tool interface {
	Spec() port.ToolSpec
	Execute(ctx context.Context, call port.ToolCall, onProgress func(string)) ToolOutput
}

// DocumentedTool 工具自带提示词贡献（pi 的 promptSnippet/promptGuidelines 模式）：
// 系统提示词的「工具使用指南」段从实际注入的工具集组装——工具增删，提示词自动跟随，
// 从结构上杜绝提示词引用已下线工具的漂移。可选接口：未实现的工具不在指南中出现。
type DocumentedTool interface {
	Tool
	PromptSnippet() string      // 一行能力说明（工具清单用）
	PromptGuidelines() []string // 使用规则（细则列表）
}

// Config loop 配置。
type Config struct {
	// MaxIterations 最大迭代轮数（一次迭代 = 一次 LLM 调用 + 其工具执行）。默认 8。
	MaxIterations int
	// RequireToolTermination 要求必须由 Terminate 工具显式收束。适用于结构化抽取等
	// 不能把“模型没有调用工具”视为成功的场景；普通对话保持自然终止语义。
	RequireToolTermination bool
	// NoToolCallReminder 在模型提前输出普通文本时作为下一轮用户消息注入。
	NoToolCallReminder string
}

// Event loop 对外事件（pi AgentEvent 子集，供 SSE 透传或日志）。
type Event struct {
	Type string // agent_start | turn_start | message_start | message_end | tool_execution_start | tool_execution_end | turn_end | agent_end
	// 工具执行事件携带
	ToolCallID string
	ToolName   string
	Args       json.RawMessage
	Output     ToolOutput
	// message 事件携带（副本）
	Message *port.Message
}

// Loop 极简 agent loop。零工具时退化为单发调用。
type Loop struct {
	llm   port.LLMClient
	tools []Tool
	cfg   Config
}

// New 构造 loop。
func New(llm port.LLMClient, tools []Tool, cfg Config) *Loop {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 8
	}
	return &Loop{llm: llm, tools: tools, cfg: cfg}
}

// Run 执行 loop。会直接修改并返回传入的 Context（追加 assistant 与 toolResult 消息），
// 调用方可将其序列化落库实现会话恢复。
// onDelta 透传模型流式增量（thinking/answer 两相位语义由事件类型区分）。
func (l *Loop) Run(
	ctx context.Context,
	cc *port.Context,
	onDelta func(port.AssistantEvent),
	onEvent func(Event),
) (*port.Context, error) {
	emit := func(ev Event) {
		if onEvent != nil {
			onEvent(ev)
		}
	}

	// 工具注册表注入 Context（provider 据此构造请求）
	l.registerTools(cc)
	emit(Event{Type: "agent_start"})

	for i := 0; i < l.cfg.MaxIterations; i++ {
		emit(Event{Type: "turn_start"})

		msg, err := l.llm.Stream(ctx, cc, onDelta)
		if msg != nil {
			cc.Messages = append(cc.Messages, *msg)
			emit(Event{Type: "message_start", Message: msg})
			emit(Event{Type: "message_end", Message: msg})
		}
		if err != nil {
			// error/aborted：保留已积累消息后短路（pi 行为）
			emit(Event{Type: "turn_end", Message: msg})
			emit(Event{Type: "agent_end"})
			return cc, err
		}
		if msg == nil {
			emit(Event{Type: "turn_end"})
			emit(Event{Type: "agent_end"})
			return cc, fmt.Errorf("agent loop: LLM 返回了空消息")
		}

		calls := msg.ToolCalls()
		if len(calls) == 0 {
			if l.cfg.RequireToolTermination {
				reminder := l.cfg.NoToolCallReminder
				if reminder == "" {
					reminder = "任务尚未通过完成工具校验。请根据当前状态继续调用工具，修正问题后显式提交完成。"
				}
				cc.Messages = append(cc.Messages, port.NewUserMessage(reminder))
				emit(Event{Type: "turn_end", Message: msg})
				continue
			}
			// 自然终止：回复不含工具调用
			emit(Event{Type: "turn_end", Message: msg})
			emit(Event{Type: "agent_end"})
			return cc, nil
		}

		// pi 生产细节：length 截断的回复中所有工具调用参数都可能不完整，
		// 一律按错误回执让模型重新发起，绝不执行
		terminate := false
		if msg.StopReason == port.StopReasonLength {
			for _, call := range calls {
				out := ToolOutput{
					Output:  fmt.Sprintf("工具调用 %q 未执行：响应达到输出上限，参数可能被截断。请以完整参数重新发起。", call.Name),
					IsError: true,
				}
				l.appendResult(cc, call, out, emit)
			}
		} else {
			for _, call := range calls {
				out := l.executeTool(ctx, call, emit)
				if out.Terminate {
					terminate = true
				}
				l.appendResult(cc, call, out, emit)
				if ctx.Err() != nil {
					emit(Event{Type: "turn_end", Message: msg})
					emit(Event{Type: "agent_end"})
					return cc, fmt.Errorf("agent loop 已中止: %w", ctx.Err())
				}
			}
		}

		emit(Event{Type: "turn_end", Message: msg})
		if terminate {
			emit(Event{Type: "agent_end"})
			return cc, nil
		}
	}

	emit(Event{Type: "agent_end"})
	return cc, fmt.Errorf("agent loop 达到最大迭代数 %d（可在 Config 调整）", l.cfg.MaxIterations)
}

func (l *Loop) registerTools(cc *port.Context) {
	if len(l.tools) == 0 {
		return
	}
	specs := make([]port.ToolSpec, 0, len(l.tools))
	for _, t := range l.tools {
		specs = append(specs, t.Spec())
	}
	cc.Tools = specs
}

func (l *Loop) executeTool(ctx context.Context, call port.ToolCall, emit func(Event)) ToolOutput {
	emit(Event{Type: "tool_execution_start", ToolCallID: call.ID, ToolName: call.Name, Args: call.Arguments})
	for _, t := range l.tools {
		if t.Spec().Name == call.Name {
			out := t.Execute(ctx, call, nil)
			emit(Event{Type: "tool_execution_end", ToolCallID: call.ID, ToolName: call.Name, Output: out})
			return out
		}
	}
	// pi 行为：未知工具按错误回执（模型可自行纠正）
	out := ToolOutput{Output: fmt.Sprintf("工具 %q 不存在", call.Name), IsError: true}
	emit(Event{Type: "tool_execution_end", ToolCallID: call.ID, ToolName: call.Name, Output: out})
	return out
}

func (l *Loop) appendResult(cc *port.Context, call port.ToolCall, out ToolOutput, emit func(Event)) {
	msg := port.NewToolResultMessage(call, out.Output, out.Details, out.IsError)
	cc.Messages = append(cc.Messages, msg)
	emit(Event{Type: "message_start", Message: &msg})
	emit(Event{Type: "message_end", Message: &msg})
}
