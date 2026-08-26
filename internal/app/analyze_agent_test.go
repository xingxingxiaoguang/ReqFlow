package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

/* ---- mock LLMClient：按脚本逐轮返回（对齐 agent 包 scriptedClient 模式） ---- */

type scriptedLLM struct {
	responses []*port.Message
	calls     int
	lastCtx   *port.Context
}

func (m *scriptedLLM) Stream(ctx context.Context, cc *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
	if m.calls >= len(m.responses) {
		return nil, fmt.Errorf("脚本耗尽（第 %d 轮调用）", m.calls+1)
	}
	msg := m.responses[m.calls]
	m.calls++
	if msg == nil { // nil 脚本 = 模拟该轮调用失败
		return nil, fmt.Errorf("模拟 LLM 调用失败（第 %d 轮）", m.calls)
	}
	snapshot := make([]port.Message, len(cc.Messages))
	copy(snapshot, cc.Messages)
	m.lastCtx = &port.Context{SystemPrompt: cc.SystemPrompt, Messages: snapshot, Tools: cc.Tools}
	if onEvent != nil {
		if msg.Text() != "" {
			onEvent(port.AssistantEvent{Type: port.EventTextDelta, Delta: msg.Text(), Message: msg})
		}
	}
	return msg, nil
}

func (m *scriptedLLM) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return m.Stream(ctx, cc, nil)
}
func (m *scriptedLLM) Ping(ctx context.Context) error { return nil }

/* ---- 桩工具 ---- */

type stubTool struct {
	fired int
}

func (t *stubTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "search_projects", Description: "桩", Parameters: json.RawMessage(`{"type":"object"}`)}
}
func (t *stubTool) Execute(ctx context.Context, call port.ToolCall, onProgress func(string)) agent.ToolOutput {
	t.fired++
	return agent.ToolOutput{Output: `[{"id":"p1","name":"用户中心","match":"exact"}]`, Details: "命中 用户中心"}
}

const testDoc = "## 需求文档内容\n\n用户中心需要实现登录功能。"
const testFinalJSON = `[{"project_name":"用户中心","title":"实现用户登录功能","description":"支持账号密码登录","priority":"High","estimated_hours":8,"type_id":"story"}]`

func toolCallMsg() *port.Message {
	c := port.ToolCall{ID: "c1", Name: "search_projects", Arguments: json.RawMessage(`{"name":"用户中心"}`)}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &c}}}
}

func finalTextMsg(text string) *port.Message {
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonStop,
		Content: []port.Block{{Type: port.BlockText, Text: text}}}
}

/* ---- agent 模式主链路 ---- */

func TestAnalyzeAgentModeFullFlow(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{toolCallMsg(), finalTextMsg(testFinalJSON)}}
	tool := &stubTool{}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode([]agent.Tool{tool}, 0)

	var tools []AnalyzeToolEvent
	var tokens []AnalyzeDelta
	var stages []string
	res, err := svc.Run(context.Background(), "需求.docx", testDoc, "",
		func(p AnalyzeProgress) { stages = append(stages, p.Stage) },
		func(d AnalyzeDelta) { tokens = append(tokens, d) },
		func(ev AnalyzeToolEvent) { tools = append(tools, ev) },
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if tool.fired != 1 {
		t.Fatalf("工具执行 %d 次", tool.fired)
	}
	if llm.calls != 2 {
		t.Fatalf("LLM 调用 %d 次，应为 2（工具轮 + 终稿轮）", llm.calls)
	}
	// 工具轨迹：start → end
	if len(tools) != 2 || tools[0].Phase != "start" || tools[1].Phase != "end" {
		t.Fatalf("tool 事件 = %+v", tools)
	}
	if tools[0].Name != "search_projects" || tools[1].Details != "命中 用户中心" {
		t.Fatalf("tool 事件内容 = %+v", tools)
	}
	// 终稿增量以 answer 相位透传
	if len(tokens) == 0 || tokens[len(tokens)-1].Phase != "answer" {
		t.Fatalf("token 流 = %+v", tokens)
	}
	// 草稿产出
	if len(res.Items) != 1 || res.Items[0].Title != "实现用户登录功能" {
		t.Fatalf("items = %+v", res.Items)
	}
	// 会话产出：含系统提示（工具指南）、工具表、user/assistant/toolResult 消息
	var cc port.Context
	if err := json.Unmarshal([]byte(res.AgentContext), &cc); err != nil {
		t.Fatalf("AgentContext 非法 JSON: %v", err)
	}
	if !strings.Contains(cc.SystemPrompt, "工具使用指南") {
		t.Fatalf("SystemPrompt 应含工具指南")
	}
	if len(cc.Tools) == 0 || cc.Tools[0].Name != "search_projects" {
		t.Fatalf("会话未记录工具表: %+v", cc.Tools)
	}
	roles := map[port.Role]int{}
	for i := range cc.Messages {
		roles[cc.Messages[i].Role]++
	}
	if roles[port.RoleUser] != 1 || roles[port.RoleToolResult] != 1 || roles[port.RoleAssistant] != 2 {
		t.Fatalf("会话消息构成 = %+v", roles)
	}
	// 第二轮调用时上下文已含工具结果（模型看得到查证结论）
	if len(llm.lastCtx.Messages) != 3 {
		t.Fatalf("第二轮消息数 = %d", len(llm.lastCtx.Messages))
	}
	// 收尾进度
	if stages[len(stages)-1] != "done" {
		t.Fatalf("末态进度 = %v", stages)
	}
}

func TestAnalyzeAgentModeDegradesToClassic(t *testing.T) {
	// loop 彻底失败（无任何输出）→ 降级单发直调成功
	llm := &scriptedLLM{responses: []*port.Message{
		nil, // agent 首轮流失败
		finalTextMsg(testFinalJSON), // 降级后的单发调用
	}}
	tool := &stubTool{}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode([]agent.Tool{tool}, 0)

	var messages []string
	res, err := svc.Run(context.Background(), "需求.docx", testDoc, "",
		func(p AnalyzeProgress) { messages = append(messages, p.Message) }, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tool.fired != 0 {
		t.Fatalf("失败轮不应执行工具，实际 %d 次", tool.fired)
	}
	if len(res.Items) != 1 {
		t.Fatalf("降级后应产出草稿: %+v", res.Items)
	}
	degraded := false
	for _, m := range messages {
		if strings.Contains(m, "回退单发") {
			degraded = true
		}
	}
	if !degraded {
		t.Fatalf("应提示降级: %v", messages)
	}
}

/* ---- 单发模式（回归：默认路径不回归） ---- */

func TestAnalyzeClassicPersistsSession(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{finalTextMsg(testFinalJSON)}}
	svc := NewAnalyzeService(llm, "")

	res, err := svc.Run(context.Background(), "需求.docx", testDoc, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %+v", res.Items)
	}
	var cc port.Context
	if err := json.Unmarshal([]byte(res.AgentContext), &cc); err != nil {
		t.Fatalf("单发模式也应产出会话: %v", err)
	}
	if len(cc.Messages) != 2 || cc.Messages[0].Role != port.RoleUser || cc.Messages[1].Role != port.RoleAssistant {
		t.Fatalf("单发会话 = %d 条消息", len(cc.Messages))
	}
	if cc.SystemPrompt != "" || len(cc.Tools) != 0 {
		t.Fatal("单发模式不应注入系统提示与工具表")
	}
}
