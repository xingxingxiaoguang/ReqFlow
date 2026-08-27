package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
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

/* ---- fake HumanAsker ---- */

type fakeAsker struct {
	asked    []string
	askedOpt [][]string
	answer   string
	block    bool // true：阻塞到 ctx 取消（模拟任务暂停时的人工等待）
}

func (f *fakeAsker) Ask(ctx context.Context, taskID, callID, question string, options []string) (string, error) {
	f.asked = append(f.asked, question)
	f.askedOpt = append(f.askedOpt, options)
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.answer, nil
}

/* ---- 脚本素材 ---- */

const testDoc = "## 需求文档内容\n\n用户中心需要实现登录功能。"
const testDraftItem = `{"project_name":"用户中心","title":"实现用户登录功能","description":"支持账号密码登录","priority":"High","estimated_hours":8,"type_id":"story"}`
const testFinalJSON = `[{"project_name":"用户中心","title":"实现用户登录功能","description":"支持账号密码登录","priority":"High","estimated_hours":8,"type_id":"story"}]`

func toolCallMsg(id, name, args string) *port.Message {
	c := port.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &c}}}
}

func finalTextMsg(text string) *port.Message {
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonStop,
		Content: []port.Block{{Type: port.BlockText, Text: text}}}
}

func analyzeInput(text string) AnalyzeInput {
	return AnalyzeInput{TaskID: "t1", FileName: "需求.docx", Text: text}
}

/* ---- agent 模式主链路：读 → 写 → 总结 ---- */

func TestAnalyzeAgentFullFlow(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{
		toolCallMsg("c1", "read_document", `{"offset":1}`),
		toolCallMsg("c2", "write_work_items", `{"items":[`+testDraftItem+`]}`),
		finalTextMsg("已产出 1 个工作项：用户中心/实现用户登录功能。"),
	}}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode(0) // 0 → 默认 32

	var toolEvents []AnalyzeToolEvent
	var tokens []AnalyzeDelta
	var stages []string
	res, err := svc.Run(context.Background(), analyzeInput(testDoc),
		func(p AnalyzeProgress) { stages = append(stages, p.Stage) },
		func(d AnalyzeDelta) { tokens = append(tokens, d) },
		func(ev AnalyzeToolEvent) { toolEvents = append(toolEvents, ev) },
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if llm.calls != 3 {
		t.Fatalf("LLM 调用 %d 次，应为 3（读取轮 + 写入轮 + 总结轮）", llm.calls)
	}
	// 工具轨迹：read 与 write 各 start/end 一对
	if len(toolEvents) != 4 {
		t.Fatalf("tool 事件数 = %d: %+v", len(toolEvents), toolEvents)
	}
	if toolEvents[0].Name != "read_document" || toolEvents[0].Phase != "start" ||
		toolEvents[2].Name != "write_work_items" || toolEvents[3].Phase != "end" {
		t.Fatalf("tool 事件 = %+v", toolEvents)
	}
	// 终稿（总结）增量以 answer 相位透传
	if len(tokens) == 0 || tokens[len(tokens)-1].Phase != "answer" {
		t.Fatalf("token 流 = %+v", tokens)
	}
	// 草稿来自写入工具的 sink（schema 字段袋）
	vals := res.Items[0].Values()
	if len(res.Items) != 1 || vals["title"] != "实现用户登录功能" || vals["project_name"] != "用户中心" {
		t.Fatalf("items = %+v", res.Items)
	}
	// 会话产出
	var cc port.Context
	if err := json.Unmarshal([]byte(res.AgentContext), &cc); err != nil {
		t.Fatalf("AgentContext 非法 JSON: %v", err)
	}
	// 系统提示从工具集组装：含指南段与实际工具名（不引用已下线的工具）
	if !strings.Contains(cc.SystemPrompt, "工具使用指南") ||
		!strings.Contains(cc.SystemPrompt, "read_document") || !strings.Contains(cc.SystemPrompt, "write_work_items") {
		t.Fatalf("SystemPrompt 缺工具指南: %.200s", cc.SystemPrompt)
	}
	if strings.Contains(cc.SystemPrompt, "search_requirements") || strings.Contains(cc.SystemPrompt, "search_projects") {
		t.Fatalf("SystemPrompt 引用了已下线工具")
	}
	if len(cc.Tools) != 4 {
		t.Fatalf("会话工具表 = %d 个", len(cc.Tools))
	}
	// 首轮 user 消息是文档清单，不含原文
	first := cc.Messages[0]
	if first.Role != port.RoleUser || !strings.Contains(first.Text(), "待分析文档") ||
		strings.Contains(first.Text(), "用户中心需要实现登录功能") {
		t.Fatalf("首轮消息应为文档清单: %.200s", first.Text())
	}
	roles := map[port.Role]int{}
	for i := range cc.Messages {
		roles[cc.Messages[i].Role]++
	}
	if roles[port.RoleUser] != 1 || roles[port.RoleToolResult] != 2 || roles[port.RoleAssistant] != 3 {
		t.Fatalf("会话消息构成 = %+v", roles)
	}
	// 读取工具的回执为带行号纯文本（行号口径与 read 一致）
	if res2 := cc.Messages[2].Result; !strings.Contains(res2, "1:## 需求文档内容") {
		t.Fatalf("read 回执应带行号: %q", res2)
	}
	// 总结轮时上下文已含全部前序消息（模型看得到读取内容与写入回执）
	if len(llm.lastCtx.Messages) != 5 {
		t.Fatalf("末轮消息数 = %d", len(llm.lastCtx.Messages))
	}
	if stages[len(stages)-1] != "done" {
		t.Fatalf("末态进度 = %v", stages)
	}
}

/* ---- agent 失败降级单发 ---- */

func TestAnalyzeAgentDegradesToClassic(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{
		nil,                         // agent 首轮流失败（sink 空 → 降级）
		finalTextMsg(testFinalJSON), // 降级后的单发调用
	}}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode(0)

	var messages []string
	res, err := svc.Run(context.Background(), analyzeInput(testDoc),
		func(p AnalyzeProgress) { messages = append(messages, p.Message) }, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
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

/* ---- agent 无产出（模型只聊天不写草稿）→ 同样降级 ---- */

func TestAnalyzeAgentNoWritesDegradesToClassic(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{
		finalTextMsg("我看过了，没什么要拆的。"), // 自然终止但 sink 空
		finalTextMsg(testFinalJSON),  // 降级后的单发调用
	}}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode(0)

	res, err := svc.Run(context.Background(), analyzeInput(testDoc), nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("降级后应产出草稿: %+v", res.Items)
	}
}

/* ---- Resume：从会话重放草稿状态后续跑 ---- */

func TestAnalyzeAgentResumeReplaysSink(t *testing.T) {
	// 检查点会话：清单 + 读取轮 + 写入轮（含回执）
	cc := &port.Context{
		SystemPrompt: "sys",
		Messages: []port.Message{
			port.NewUserMessage("## 待分析文档\n…"),
			*toolCallMsg("c1", "read_document", `{"offset":1}`),
			port.NewToolResultMessage(port.ToolCall{ID: "c1", Name: "read_document"}, "1:…", "", false),
			*toolCallMsg("c2", "write_work_items", `{"items":[`+testDraftItem+`]}`),
			port.NewToolResultMessage(port.ToolCall{ID: "c2", Name: "write_work_items"},
				`{"accepted":1,"updated":0,"total_in_draft":1}`, "", false),
		},
	}
	llm := &scriptedLLM{responses: []*port.Message{finalTextMsg("续跑完成")}}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode(0)

	res, err := svc.Resume(context.Background(), cc, analyzeInput(testDoc), nil, nil, nil)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// 本轮无新写入：items 完全来自会话重放
	if len(res.Items) != 1 || res.Items[0].Values()["title"] != "实现用户登录功能" {
		t.Fatalf("重放后的 items = %+v", res.Items)
	}
	if llm.calls != 1 {
		t.Fatalf("续跑应只调 1 轮，实际 %d", llm.calls)
	}
}

/* ---- ask_human：问题与选项透传，回答进入会话 ---- */

func TestAnalyzeAgentAskHuman(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{
		toolCallMsg("c9", "ask_human", `{"question":"部署环境是内部还是公有云？","options":["内部","公有云"]}`),
		toolCallMsg("c2", "write_work_items", `{"items":[`+testDraftItem+`]}`),
		finalTextMsg("完成"),
	}}
	asker := &fakeAsker{answer: "内部"}
	svc := NewAnalyzeService(llm, "")
	svc.EnableAgentMode(0)
	in := analyzeInput(testDoc)
	in.Dialog = asker

	res, err := svc.Run(context.Background(), in, nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(asker.asked) != 1 || asker.asked[0] != "部署环境是内部还是公有云？" {
		t.Fatalf("asker 收到 = %+v", asker.asked)
	}
	if len(asker.askedOpt[0]) != 2 {
		t.Fatalf("选项未透传: %+v", asker.askedOpt[0])
	}
	// 回答进入会话（写入轮的模型看得到）
	var cc port.Context
	if err := json.Unmarshal([]byte(res.AgentContext), &cc); err != nil {
		t.Fatalf("AgentContext: %v", err)
	}
	found := false
	for _, m := range cc.Messages {
		if m.Role == port.RoleToolResult && m.ToolCallID == "c9" {
			found = true
			if !strings.Contains(m.Result, "内部") {
				t.Fatalf("回执缺人工回答: %q", m.Result)
			}
		}
	}
	if !found {
		t.Fatal("会话中未找到 ask_human 的回执")
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %+v", res.Items)
	}
}

/* ---- 单发模式（回归：默认路径不注入工具与清单） ---- */

func TestAnalyzeClassicPersistsSession(t *testing.T) {
	llm := &scriptedLLM{responses: []*port.Message{finalTextMsg(testFinalJSON)}}
	svc := NewAnalyzeService(llm, "")

	res, err := svc.Run(context.Background(), analyzeInput(testDoc), nil, nil, nil)
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
	// 单发 prompt：指令 + JSON 输出契约 + 文档原文同一条 user 消息
	prompt := cc.Messages[0].Text()
	if !strings.Contains(prompt, "只输出 JSON 数组") || !strings.Contains(prompt, "用户中心需要实现登录功能") {
		t.Fatalf("单发 prompt 形状异常: %.200s", prompt)
	}
}

/* ---- 提示词动态装配（profile 注册表 + schema 同源渲染）---- */

func TestPromptAssembledFromProfileAndSchema(t *testing.T) {
	profile, ok := AnalyzeProfileOf(model.TaskTypeRequirementImport)
	if !ok {
		t.Fatal("requirement_import 应已注册 profile")
	}
	now := time.Now()

	// 指令头：字段规范段来自 schema 渲染（字段名/枚举值域/必填标注/提取说明/时间占位）
	head := renderAnalyzeHead(now, profile)
	for _, want := range []string{
		"草稿字段规范",
		"**title** (string，必填)",
		`**priority** (string，必须为 "High"/"Medium"/"Low" 之一)`,
		"estimated_hours",
		"动宾结构",                   // FieldSpec.Prompt 的提取说明
		now.Format(time.RFC3339), // {current_time} 已填充
	} {
		if !strings.Contains(head, want) {
			t.Fatalf("指令头缺 %q:\n%s", want, head)
		}
	}

	// agent SystemPrompt：指令头 + 工具指南同源组装
	toolset := buildToolset(analyzeInput(testDoc), tools.NewDraftSink(), profile)
	sys := renderAgentSystem(now, "", toolset, profile)
	if !strings.Contains(sys, "工具使用指南") || !strings.Contains(sys, "read_document") {
		t.Fatalf("SystemPrompt = %.300s", sys)
	}

	// 单发契约：通用契约文本 + profile 富示例
	classic := renderClassicOutputFormat(now, profile)
	if !strings.Contains(classic, "只输出 JSON 数组") || !strings.Contains(classic, "实现用户注册功能") {
		t.Fatalf("单发契约 = %.200s", classic)
	}

	// profile 解析：空类型回退默认；未注册类型报错
	if _, err := profileFor(""); err != nil {
		t.Fatalf("空类型应回退默认: %v", err)
	}
	if _, err := profileFor("no_such_type"); err == nil {
		t.Fatal("未注册类型应报错")
	}
}

func TestPromptExampleSkeletonFromSchema(t *testing.T) {
	// 无 Example 的 profile：示例骨架从 schema 生成（必填字段占位、选填省略）
	profile := AnalyzeProfile{
		Role: "R\n\n{field_spec}",
		Schema: func() model.DatasetSchema {
			return model.DatasetSchema{Type: "demo", Fields: []model.FieldSpec{
				{Key: "title", Label: "标题", Type: model.FieldString, Required: true},
				{Key: "level", Label: "级别", Type: model.FieldEnum, Enum: []string{"p0", "p1"}, Required: true},
				{Key: "hours", Label: "工时", Type: model.FieldNumber, Required: true},
				{Key: "opt", Label: "选填", Type: model.FieldString},
			}}
		},
	}
	ex := renderClassicOutputFormat(time.Now(), profile)
	for _, want := range []string{`"title"`, `"level": "p0"`, `"hours": 1`} {
		if !strings.Contains(ex, want) {
			t.Fatalf("骨架缺 %s: %s", want, ex)
		}
	}
	if strings.Contains(ex, `"opt"`) {
		t.Fatal("选填字段不应进骨架")
	}
	// 字段规范段同样由该 schema 渲染（提示词与写入校验同一事实源）
	head := renderAnalyzeHead(time.Now(), profile)
	if !strings.Contains(head, "**hours** (number") || !strings.Contains(head, "**title** (string，必填)") {
		t.Fatalf("字段规范段 = %s", head)
	}
}
