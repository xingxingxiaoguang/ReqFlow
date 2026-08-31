package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"reqflow/internal/port"
)

/* ---- mock LLMClient：按脚本逐轮返回 ---- */

type scriptedClient struct {
	responses []*port.Message // 每轮 Stream 的返回
	calls     int
	lastCtx   *port.Context // 最近一次调用收到的 Context 快照
}

func (m *scriptedClient) Stream(ctx context.Context, cc *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
	if m.calls >= len(m.responses) {
		return nil, fmt.Errorf("脚本耗尽（第 %d 轮调用）", m.calls+1)
	}
	msg := m.responses[m.calls]
	m.calls++
	// 快照当前消息序列（cc 会被 loop 继续追加，不能直接存指针）
	snapshot := make([]port.Message, len(cc.Messages))
	copy(snapshot, cc.Messages)
	m.lastCtx = &port.Context{SystemPrompt: cc.SystemPrompt, Messages: snapshot, Tools: cc.Tools}
	if onEvent != nil {
		onEvent(port.AssistantEvent{Type: port.EventTextDelta, Delta: msg.Text(), Message: msg})
	}
	return msg, nil
}
func (m *scriptedClient) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return m.Stream(ctx, cc, nil)
}
func (m *scriptedClient) Ping(ctx context.Context) error { return nil }

func assistantText(text string) *port.Message {
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonStop,
		Content: []port.Block{{Type: port.BlockText, Text: text}}}
}

func assistantToolCalls(stop port.StopReason, calls ...port.ToolCall) *port.Message {
	m := &port.Message{Role: port.RoleAssistant, StopReason: stop}
	for i := range calls {
		c := calls[i]
		m.Content = append(m.Content, port.Block{Type: port.BlockToolCall, ToolCall: &c})
	}
	return m
}

/* ---- mock 工具 ---- */

type echoTool struct {
	name     string
	output   string
	fail     bool
	fired    int
	lastArgs json.RawMessage
}

func (t *echoTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: t.name, Description: "测试工具", Parameters: json.RawMessage(`{"type":"object"}`)}
}
func (t *echoTool) Execute(ctx context.Context, call port.ToolCall, onProgress func(string)) ToolOutput {
	t.fired++
	t.lastArgs = call.Arguments
	if t.fail {
		return ToolOutput{Output: "执行失败", IsError: true}
	}
	return ToolOutput{Output: t.output, Details: "UI 展示用"}
}

func runLoop(t *testing.T, client *scriptedClient, tools []Tool, cfg Config) (*port.Context, []Event, error) {
	t.Helper()
	cc := &port.Context{Messages: []port.Message{port.NewUserMessage("开始")}}
	var events []Event
	final, err := New(client, tools, cfg).Run(context.Background(), cc, nil, func(ev Event) { events = append(events, ev) })
	return final, events, err
}

func TestLoopSingleTurnNaturalStop(t *testing.T) {
	client := &scriptedClient{responses: []*port.Message{assistantText("[{\"title\":\"完成\"}]")}}
	final, events, err := runLoop(t, client, nil, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("LLM 调用 %d 次，无工具应只调 1 次", client.calls)
	}
	if final.Messages[len(final.Messages)-1].Role != port.RoleAssistant {
		t.Fatalf("末尾消息应为 assistant")
	}
	if len(final.Tools) != 0 {
		t.Fatalf("零工具时不应注册 Tools")
	}
	if !hasEvent(events, "agent_end") {
		t.Fatalf("缺少 agent_end")
	}
}

func TestLoopToolExecutionAndSecondTurn(t *testing.T) {
	tool := &echoTool{name: "search", output: "搜索结果"}
	client := &scriptedClient{responses: []*port.Message{
		assistantToolCalls(port.StopReasonToolUse, port.ToolCall{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"登录"}`)}),
		assistantText("基于搜索结果的最终答案"),
	}}
	final, _, err := runLoop(t, client, []Tool{tool}, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tool.fired != 1 {
		t.Fatalf("工具执行 %d 次", tool.fired)
	}
	if string(tool.lastArgs) != `{"q":"登录"}` {
		t.Fatalf("工具参数 = %s", tool.lastArgs)
	}
	// 消息序列：user → assistant(调用) → toolResult → assistant(终答)
	roles := []port.Role{}
	for i := range final.Messages {
		roles = append(roles, final.Messages[i].Role)
	}
	want := []port.Role{port.RoleUser, port.RoleAssistant, port.RoleToolResult, port.RoleAssistant}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Fatalf("消息序列 = %v", roles)
	}
	// 工具 spec 注入了 Context（provider 可见）
	if len(final.Tools) != 1 || final.Tools[0].Name != "search" {
		t.Fatalf("Tools = %+v", final.Tools)
	}
	// 第二轮调用时上下文已包含工具结果
	secondCtx := client.lastCtx
	if len(secondCtx.Messages) != 3 {
		t.Fatalf("第二轮消息数 = %d", len(secondCtx.Messages))
	}
}

func TestLoopLengthFailsAllToolCallsWithoutExecution(t *testing.T) {
	// pi 生产细节：length 截断的工具调用一律不执行，按错误回执
	tool := &echoTool{name: "search", output: "x"}
	client := &scriptedClient{responses: []*port.Message{
		assistantToolCalls(port.StopReasonLength,
			port.ToolCall{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"可能被截`)},
			port.ToolCall{ID: "c2", Name: "search", Arguments: json.RawMessage(`{}`)}),
		assistantText("重新发起后的答案"),
	}}
	final, _, err := runLoop(t, client, []Tool{tool}, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tool.fired != 0 {
		t.Fatalf("length 截断的工具调用不应执行，实际执行 %d 次", tool.fired)
	}
	// 两条错误 toolResult 均入列
	errResults := 0
	for i := range final.Messages {
		if final.Messages[i].Role == port.RoleToolResult && final.Messages[i].IsError {
			errResults++
		}
	}
	if errResults != 2 {
		t.Fatalf("错误回执 = %d, want 2", errResults)
	}
}

func TestLoopUnknownToolReportsError(t *testing.T) {
	client := &scriptedClient{responses: []*port.Message{
		assistantToolCalls(port.StopReasonToolUse, port.ToolCall{ID: "c1", Name: "不存在", Arguments: json.RawMessage(`{}`)}),
		assistantText("收到工具不存在后的纠正答案"),
	}}
	final, _, err := runLoop(t, client, []Tool{&echoTool{name: "别的"}}, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	last2 := final.Messages[len(final.Messages)-2]
	if last2.Role != port.RoleToolResult || !last2.IsError {
		t.Fatalf("未知工具应产生错误回执: %+v", last2)
	}
}

func TestLoopMaxIterationsGuard(t *testing.T) {
	// 模型永远请求工具调用：安全阀必须在 MaxIterations 轮后停下
	always := make([]*port.Message, 10)
	for i := range always {
		always[i] = assistantToolCalls(port.StopReasonToolUse, port.ToolCall{ID: fmt.Sprintf("c%d", i), Name: "t", Arguments: json.RawMessage(`{}`)})
	}
	client := &scriptedClient{responses: always}
	_, _, err := runLoop(t, client, []Tool{&echoTool{name: "t"}}, Config{MaxIterations: 3})
	if err == nil {
		t.Fatal("应触发迭代上限错误")
	}
	if client.calls != 3 {
		t.Fatalf("LLM 调用 %d 次, want 3", client.calls)
	}
}

// 直接验证 terminate 语义
type doneTool struct{ echoTool }

func (d *doneTool) Execute(ctx context.Context, call port.ToolCall, onProgress func(string)) ToolOutput {
	d.fired++
	return ToolOutput{Output: "收束", Terminate: true}
}

func TestLoopTerminateToolStops(t *testing.T) {
	client := &scriptedClient{responses: []*port.Message{
		assistantToolCalls(port.StopReasonToolUse, port.ToolCall{ID: "c1", Name: "done", Arguments: json.RawMessage(`{}`)}),
		assistantText("不应到达"),
	}}
	final, _, err := runLoop(t, client, []Tool{&doneTool{echoTool{name: "done"}}}, Config{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("terminate 工具后不应再调用 LLM（实际 %d 次）", client.calls)
	}
	if len(final.Messages) != 3 { // user + assistant + toolResult
		t.Fatalf("消息数 = %d", len(final.Messages))
	}
}

func TestLoopCanRequireExplicitTerminateTool(t *testing.T) {
	client := &scriptedClient{responses: []*port.Message{
		assistantText("我认为已经完成"),
		assistantToolCalls(port.StopReasonToolUse,
			port.ToolCall{ID: "c1", Name: "done", Arguments: json.RawMessage(`{}`)}),
	}}
	done := &doneTool{echoTool{name: "done"}}
	final, _, err := runLoop(t, client, []Tool{done}, Config{
		MaxIterations: 3, RequireToolTermination: true,
		NoToolCallReminder: "必须调用 done",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.calls != 2 || done.fired != 1 {
		t.Fatalf("calls=%d done=%d", client.calls, done.fired)
	}
	foundReminder := false
	for _, message := range final.Messages {
		if message.Role == port.RoleUser && message.Text() == "必须调用 done" {
			foundReminder = true
		}
	}
	if !foundReminder {
		t.Fatal("缺少显式完成提醒")
	}
}

func TestContextSerializableRoundTrip(t *testing.T) {
	// Context 即会话：序列化/反序列化必须无损（refine 与落库的前提）
	call := port.ToolCall{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)}
	cc := port.Context{
		SystemPrompt: "sys",
		Tools:        []port.ToolSpec{{Name: "search", Description: "d", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Messages: []port.Message{
			port.NewUserMessage("问题"),
			{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse, Content: []port.Block{
				{Type: port.BlockThinking, Thinking: "想", ThinkingSignature: "sig"},
				{Type: port.BlockText, Text: "查"},
				{Type: port.BlockToolCall, ToolCall: &call},
			}},
			port.NewToolResultMessage(call, "结果", "UI", true),
		},
	}
	raw, err := json.Marshal(&cc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored port.Context
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if restored.SystemPrompt != "sys" || len(restored.Messages) != 3 {
		t.Fatalf("往返丢失: %+v", restored)
	}
	assistant := restored.Messages[1]
	if assistant.Text() != "查" || assistant.Content[0].ThinkingSignature != "sig" {
		t.Fatalf("assistant 往返失真: %+v", assistant.Content)
	}
	if calls := assistant.ToolCalls(); len(calls) != 1 || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Fatalf("toolcall 往返失真: %+v", calls)
	}
	if restored.Messages[2].Details != "UI" || !restored.Messages[2].IsError {
		t.Fatalf("toolResult 往返失真: %+v", restored.Messages[2])
	}
}

func hasEvent(events []Event, typ string) bool {
	for _, e := range events {
		if e.Type == typ {
			return true
		}
	}
	return false
}
