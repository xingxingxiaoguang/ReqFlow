package agent

import (
	"context"
	"fmt"
	"sort"
	"time"

	"reqflow/internal/port"
)

const defaultTraceFlushInterval = 250 * time.Millisecond

// Usage 是 Agent 会话累计用量的领域无关表示。
type Usage struct {
	RequestCount     int `json:"request_count,omitempty"`
	InputTokens      int `json:"input_tokens,omitempty"`
	OutputTokens     int `json:"output_tokens,omitempty"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// RunState 是所有流程节点共享的可恢复 Agent 状态。节点自己的领域状态应与它并列存放，
// 不应再复制一套 Context、用量和恢复协议。
type RunState struct {
	Context        port.Context `json:"context"`
	AccountedUsage Usage        `json:"accounted_usage,omitempty"`
}

type ToolTrace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
	Details string `json:"details,omitempty"`
}

// RunTrace 是给任务详情页和调试工具使用的通用运行视图。
type RunTrace struct {
	ID           string         `json:"id"`
	Label        string         `json:"label"`
	Ordinal      int            `json:"ordinal"`
	Status       string         `json:"status"`
	Thinking     string         `json:"thinking,omitempty"`
	Output       string         `json:"output,omitempty"`
	Tools        []ToolTrace    `json:"tools,omitempty"`
	RequestCount int            `json:"request_count"`
	Stats        map[string]int `json:"stats,omitempty"`
	UpdatedAt    int64          `json:"updated_at"`
}

// TraceEnvelope 嵌入节点 checkpoint，使编排层只认一个稳定的 agent_runs 协议。
type TraceEnvelope struct {
	AgentRuns []RunTrace `json:"agent_runs,omitempty"`
}

type TraceOptions struct {
	// ExposeToolResult 决定成功工具结果是否可进入 UI trace。错误结果始终暴露；
	// 读取原文、检索等可能很大的结果通常应隐藏。
	ExposeToolResult func(toolName string) bool
}

type RunOptions struct {
	ID            string
	Label         string
	Ordinal       int
	Loop          Config
	FlushInterval time.Duration
	Stats         func() map[string]int
	Trace         TraceOptions
	// BeforeFlush 让领域节点先把局部状态放回自己的 checkpoint envelope。
	BeforeFlush func()
	// OnFlush 在 trace 和领域状态都更新后持久化 checkpoint、上报进度。
	OnFlush func(RunTrace) error
}

// Execute 运行一个可恢复 Agent，并统一生成实时 trace。工具错误仍由 Loop 作为可纠正
// 反馈写回上下文；只有 LLM/上下文取消、checkpoint 或进度回调失败才中止本次运行。
func Execute(ctx context.Context, llm port.LLMClient, tools []Tool, state *RunState,
	traces *TraceEnvelope, options RunOptions) error {
	if llm == nil || state == nil || traces == nil {
		return fmt.Errorf("agent run: llm、state 和 traces 不能为空")
	}
	if options.ID == "" {
		return fmt.Errorf("agent run: id 不能为空")
	}
	if options.Label == "" {
		options.Label = options.ID
	}
	if options.FlushInterval <= 0 {
		options.FlushInterval = defaultTraceFlushInterval
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	lastFlush := time.Time{}
	partialThinking, partialOutput := "", ""
	var callbackErr error

	flush := func(force bool, status string) {
		if callbackErr != nil {
			return
		}
		if !force && time.Since(lastFlush) < options.FlushInterval {
			return
		}
		lastFlush = time.Now()
		trace := BuildTrace(options.ID, options.Label, options.Ordinal, status, &state.Context,
			partialThinking, partialOutput, options.Stats, options.Trace)
		UpsertTrace(traces, trace)
		if options.BeforeFlush != nil {
			options.BeforeFlush()
		}
		if options.OnFlush != nil {
			if err := options.OnFlush(trace); err != nil {
				callbackErr = err
				cancel()
			}
		}
	}

	flush(true, "running")
	if callbackErr != nil {
		return callbackErr
	}
	loop := New(llm, tools, options.Loop)
	finalContext, runErr := loop.Run(runCtx, &state.Context, func(event port.AssistantEvent) {
		switch event.Type {
		case port.EventThinkingDelta:
			partialThinking += event.Delta
		case port.EventTextDelta:
			partialOutput += event.Delta
		}
		flush(false, "running")
	}, func(event Event) {
		switch event.Type {
		case "message_end":
			partialThinking, partialOutput = "", ""
			flush(true, "running")
		case "tool_execution_start", "tool_execution_end":
			flush(true, "running")
		}
	})
	if finalContext != nil {
		state.Context = *finalContext
	}
	status := "succeeded"
	if runErr != nil || callbackErr != nil {
		status = "failed"
	}
	flush(true, status)
	if callbackErr != nil {
		return callbackErr
	}
	return runErr
}

func BuildTrace(id, label string, ordinal int, status string, ctx *port.Context,
	partialThinking, partialOutput string, stats func() map[string]int, options TraceOptions) RunTrace {
	trace := RunTrace{ID: id, Label: label, Ordinal: ordinal, Status: status,
		UpdatedAt: time.Now().UnixMilli()}
	toolIndex := map[string]int{}
	if ctx != nil {
		for _, message := range ctx.Messages {
			switch message.Role {
			case port.RoleAssistant:
				trace.RequestCount++
				for _, block := range message.Content {
					switch block.Type {
					case port.BlockThinking:
						appendTrace(&trace.Thinking, block.Thinking)
					case port.BlockText:
						appendTrace(&trace.Output, block.Text)
					case port.BlockToolCall:
						if block.ToolCall != nil {
							toolIndex[block.ToolCall.ID] = len(trace.Tools)
							trace.Tools = append(trace.Tools, ToolTrace{ID: block.ToolCall.ID,
								Name: block.ToolCall.Name, Args: truncateTrace(string(block.ToolCall.Arguments), 6000),
								Status: "running"})
						}
					}
				}
			case port.RoleToolResult:
				if index, ok := toolIndex[message.ToolCallID]; ok {
					tool := &trace.Tools[index]
					tool.Status = "done"
					if message.IsError {
						tool.Status = "error"
					}
					if message.IsError || (options.ExposeToolResult != nil && options.ExposeToolResult(tool.Name)) {
						tool.Result = truncateTrace(message.Result, 4000)
					}
					tool.Details = truncateTrace(message.Details, 2000)
				}
			}
		}
	}
	appendTrace(&trace.Thinking, partialThinking)
	appendTrace(&trace.Output, partialOutput)
	trace.Thinking = truncateTrace(trace.Thinking, 50000)
	trace.Output = truncateTrace(trace.Output, 50000)
	if len(trace.Tools) > 100 {
		trace.Tools = append([]ToolTrace(nil), trace.Tools[len(trace.Tools)-100:]...)
	}
	if stats != nil {
		trace.Stats = stats()
	}
	return trace
}

func UpsertTrace(envelope *TraceEnvelope, trace RunTrace) {
	for i := range envelope.AgentRuns {
		if envelope.AgentRuns[i].ID == trace.ID {
			envelope.AgentRuns[i] = trace
			return
		}
	}
	envelope.AgentRuns = append(envelope.AgentRuns, trace)
	sort.SliceStable(envelope.AgentRuns, func(i, j int) bool {
		return envelope.AgentRuns[i].Ordinal < envelope.AgentRuns[j].Ordinal
	})
}

func ContextUsage(ctx *port.Context) Usage {
	var usage Usage
	if ctx == nil {
		return usage
	}
	for _, message := range ctx.Messages {
		usage.InputTokens += message.Usage.Input
		usage.OutputTokens += message.Usage.Output
		usage.CacheReadTokens += message.Usage.CacheRead
		usage.CacheWriteTokens += message.Usage.CacheWrite
		if message.Role == port.RoleAssistant {
			usage.RequestCount++
		}
	}
	return usage
}

func UnaccountedUsage(ctx *port.Context, accounted Usage) Usage {
	total := ContextUsage(ctx)
	return Usage{RequestCount: max(0, total.RequestCount-accounted.RequestCount),
		InputTokens:      max(0, total.InputTokens-accounted.InputTokens),
		OutputTokens:     max(0, total.OutputTokens-accounted.OutputTokens),
		CacheReadTokens:  max(0, total.CacheReadTokens-accounted.CacheReadTokens),
		CacheWriteTokens: max(0, total.CacheWriteTokens-accounted.CacheWriteTokens)}
}

func AddUsage(left, right Usage) Usage {
	return Usage{RequestCount: left.RequestCount + right.RequestCount,
		InputTokens:      left.InputTokens + right.InputTokens,
		OutputTokens:     left.OutputTokens + right.OutputTokens,
		CacheReadTokens:  left.CacheReadTokens + right.CacheReadTokens,
		CacheWriteTokens: left.CacheWriteTokens + right.CacheWriteTokens}
}

func appendTrace(destination *string, value string) {
	if value == "" {
		return
	}
	if *destination != "" {
		*destination += "\n\n"
	}
	*destination += value
}

func truncateTrace(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "… [已截断]"
}
