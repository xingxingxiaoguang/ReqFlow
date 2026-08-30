// port/llm.go 的消息模型与事件协议移植自 pi（https://github.com/earendil-works/pi，
// MIT License, Copyright (c) 2025 Mario Zechner）的 packages/ai/src/types.ts。
// 有意偏离：回调事件替代 async iterator（Go 惯例，中止走 ctx）；不移植模型注册表、
// 厂商 compat 矩阵、图片块与 deferred 响应——"if I don't need it, it won't be built"。

package port

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Role 统一消息角色（pi Message 三角色）。
type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// StopReason 终止原因（pi StopReason，去掉 v1 用不到的 deferred）。
type StopReason string

const (
	StopReasonPending StopReason = "pending"
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"  // 输出被 token 上限截断（工具调用参数可能不完整）
	StopReasonToolUse StopReason = "toolUse" // 模型请求工具调用
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// BlockType assistant 内容块类型。
type BlockType string

const (
	BlockText     BlockType = "text"
	BlockThinking BlockType = "thinking"
	BlockToolCall BlockType = "toolCall"
)

// ToolCall 工具调用。Arguments 保持原始 JSON（执行侧自行解析）。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Block 内容块：Text / Thinking / ToolCall 三者按 Type 取用。
// ThinkingSignature 为 Anthropic 思考块签名——回放 assistant 消息时必须原样带回。
type Block struct {
	Type              BlockType `json:"type"`
	Text              string    `json:"text,omitempty"`
	Thinking          string    `json:"thinking,omitempty"`
	ThinkingSignature string    `json:"thinking_signature,omitempty"`
	ToolCall          *ToolCall `json:"tool_call,omitempty"`
}

// Usage token 用量（best-effort，各厂商报告时机不一）。
type Usage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

// Message 统一消息：user/assistant 携带 Content 块；toolResult 携带工具执行结果。
// 全字段可 JSON 序列化——Context 即会话，落库/恢复/换模型续跑都以它为单位。
type Message struct {
	Role         Role       `json:"role"`
	Content      []Block    `json:"content,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"` // toolResult
	ToolName     string     `json:"tool_name,omitempty"`    // toolResult
	Result       string     `json:"result,omitempty"`       // toolResult：给 LLM 的文本
	Details      string     `json:"details,omitempty"`      // toolResult：给 UI 的展示（不进 LLM）
	IsError      bool       `json:"is_error,omitempty"`
	StopReason   StopReason `json:"stop_reason,omitempty"` // assistant
	Usage        Usage      `json:"usage,omitempty"`       // assistant
	ErrorMessage string     `json:"error_message,omitempty"`
	Timestamp    int64      `json:"timestamp,omitempty"`
}

// Text 拼接全部 text 块（结构化解析取这里）。
func (m *Message) Text() string {
	var sb strings.Builder
	for i := range m.Content {
		if m.Content[i].Type == BlockText {
			sb.WriteString(m.Content[i].Text)
		}
	}
	return sb.String()
}

// ToolCalls 收集全部工具调用块。
func (m *Message) ToolCalls() []ToolCall {
	var out []ToolCall
	for i := range m.Content {
		if m.Content[i].Type == BlockToolCall && m.Content[i].ToolCall != nil {
			out = append(out, *m.Content[i].ToolCall)
		}
	}
	return out
}

// NewUserMessage 构造纯文本用户消息。
func NewUserMessage(text string) Message {
	return Message{
		Role:      RoleUser,
		Content:   []Block{{Type: BlockText, Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewToolResultMessage 构造工具结果消息。
// result 给 LLM；details 仅给 UI 展示，provider 回放时不得带上。
func NewToolResultMessage(call ToolCall, result, details string, isError bool) Message {
	return Message{
		Role:       RoleToolResult,
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Result:     result,
		Details:    details,
		IsError:    isError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// ToolSpec 工具定义（Parameters 为 JSON Schema）。
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Context pi Context 的 Go 化：一份完整可序列化的会话状态。
// 换 provider 不变形；refine 微调会话 = 在它末尾追加消息后重放。
type Context struct {
	SystemPrompt string     `json:"system_prompt,omitempty"`
	Messages     []Message  `json:"messages"`
	Tools        []ToolSpec `json:"tools,omitempty"`
	// TaskSchema 任务产出 schema 的快照（M3 元数据受控编辑的快照隔离）：
	// 随会话检查点落库，Resume 重放草稿写入时按执行时的 schema 校验/归一化/定 key
	// ——元数据热编辑不影响进行中任务（METADATA §4.5）。JSON 文本，空 = 按 effective。
	TaskSchema string `json:"task_schema,omitempty"`
}

/* ---- 流事件协议（pi AssistantMessageEvent 子集）---- */

// EventType 事件类型。start 先于一切增量；done/error 收尾并携带终态消息。
type EventType string

const (
	EventStart         EventType = "start"
	EventTextDelta     EventType = "text_delta"
	EventThinkingDelta EventType = "thinking_delta"
	EventToolCallDelta EventType = "toolcall_delta"
	EventToolCallEnd   EventType = "toolcall_end"
	EventDone          EventType = "done"
	EventError         EventType = "error"
)

// AssistantEvent 流事件。Message 指向正在构建中的 assistant 消息（增量期间为部分态）。
type AssistantEvent struct {
	Type         EventType `json:"type"`
	Delta        string    `json:"delta,omitempty"` // text/thinking/toolcall 参数增量
	ContentIndex int       `json:"content_index,omitempty"`
	ToolCall     *ToolCall `json:"tool_call,omitempty"` // toolcall_end 携带
	Message      *Message  `json:"message"`
}

// LLMClient 对话模型客户端（pi StreamFunction 的 Go 化契约）。
// 第二波 bug 定级、微调会话、agent loop 均在同一契约上扩展，不新增依赖边。
type LLMClient interface {
	// Stream 流式对话：返回最终 assistant 消息（Content 块 + StopReason + Usage）。
	// 失败时返回已积累的部分消息和 error；中止通过 ctx 取消，StopReason=aborted。
	Stream(ctx context.Context, c *Context, onEvent func(AssistantEvent)) (*Message, error)
	// Complete 非流式对话（流式解析失败时的回退通道）。
	Complete(ctx context.Context, c *Context) (*Message, error)
	// Ping 连通性测试。
	Ping(ctx context.Context) error
}
