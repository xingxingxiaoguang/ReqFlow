package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

/* ---- 测试辅助 ---- */

func executeTool(t *testing.T, ts []agent.Tool, name, args string) agent.ToolOutput {
	t.Helper()
	for _, tool := range ts {
		if tool.Spec().Name == name {
			return tool.Execute(context.Background(),
				port.ToolCall{ID: "c1", Name: name, Arguments: json.RawMessage(args)}, nil)
		}
	}
	t.Fatalf("工具 %s 不存在", name)
	return agent.ToolOutput{}
}

// executeToolCtx 携自定义 ctx（取消场景）。
func executeToolCtx(t *testing.T, ts []agent.Tool, ctx context.Context, name, args string) agent.ToolOutput {
	t.Helper()
	for _, tool := range ts {
		if tool.Spec().Name == name {
			return tool.Execute(ctx, port.ToolCall{ID: "c1", Name: name, Arguments: json.RawMessage(args)}, nil)
		}
	}
	t.Fatalf("工具 %s 不存在", name)
	return agent.ToolOutput{}
}

func runDeps(text string, ask HumanAsker) RunDeps {
	return RunDeps{Doc: DocSource{FileName: "需求.md", Text: text}, Sink: NewDraftSink(), TaskID: "t1", Ask: ask}
}

// fakeAsker 即答型人工交互桩。
type fakeAsker struct{ answer string }

func (f *fakeAsker) Ask(ctx context.Context, taskID, callID, question string, options []string) (string, error) {
	return f.answer, nil
}

// blockingAsker 阻塞到 ctx 取消（模拟任务暂停时的人工等待）。
type blockingAsker struct{}

func (blockingAsker) Ask(ctx context.Context, taskID, callID, question string, options []string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

/* ---- read_document ---- */

func TestReadDocumentPaging(t *testing.T) {
	text := "l1\nl2\nl3\nl4\nl5"
	ts := BuildForRun(runDeps(text, nil))

	// 默认读取：全文带行号，无续读提示（读完即无提示）
	out := executeTool(t, ts, "read_document", `{}`)
	if out.IsError || !strings.Contains(out.Output, "1:l1") || !strings.Contains(out.Output, "5:l5") {
		t.Fatalf("输出 = %q", out.Output)
	}
	if strings.Contains(out.Output, "继续读取") {
		t.Fatalf("全文读完后不应有续读提示: %q", out.Output)
	}

	// 中段分页：行号前缀 + 行动性续读提示
	out = executeTool(t, ts, "read_document", `{"offset":2,"limit":2}`)
	if !strings.Contains(out.Output, "2:l2") || !strings.Contains(out.Output, "3:l3") || strings.Contains(out.Output, "l1") {
		t.Fatalf("分页输出 = %q", out.Output)
	}
	if !strings.Contains(out.Output, "已显示第 2-3 行，共 5 行。用 offset=4 继续读取") {
		t.Fatalf("缺续读提示: %q", out.Output)
	}

	// 越界：错误回执附总行数
	out = executeTool(t, ts, "read_document", `{"offset":99}`)
	if !out.IsError || !strings.Contains(out.Output, "共 5 行") {
		t.Fatalf("越界回执 = %+v", out)
	}
}

func TestReadDocumentLongLineHardSplit(t *testing.T) {
	// 单行超过字符上限：按 rune 硬拆并显式提示剩余量与检索指引
	long := strings.Repeat("长", readMaxRunes+500)
	ts := BuildForRun(runDeps(long, nil))
	out := executeTool(t, ts, "read_document", `{}`)
	if out.IsError {
		t.Fatalf("超长行不应报错: %+v", out)
	}
	if !strings.Contains(out.Output, "超长已截断") || !strings.Contains(out.Output, "search_document") {
		t.Fatalf("缺硬拆提示: %.200s", out.Output)
	}
}

/* ---- search_document ---- */

func TestSearchDocumentMatchAndContext(t *testing.T) {
	text := "alpha\nbeta\ngamma\nbeta2\n"
	ts := BuildForRun(runDeps(text, nil))

	out := executeTool(t, ts, "search_document", `{"pattern":"beta","context":1}`)
	if out.IsError {
		t.Fatalf("检索失败: %+v", out)
	}
	// grep 格式：命中行「N:」、上下文行「N-」；行号与 read 一致
	if !strings.Contains(out.Output, "2:beta") || !strings.Contains(out.Output, "1-alpha") || !strings.Contains(out.Output, "3-gamma") {
		t.Fatalf("输出 = %q", out.Output)
	}
	if !strings.Contains(out.Output, "4:beta2") {
		t.Fatalf("缺第二命中: %q", out.Output)
	}
}

func TestSearchDocumentModifiers(t *testing.T) {
	text := "版本 3.1.2 已发布\n版本 35102 内部\nConfig File\n"
	ts := BuildForRun(runDeps(text, nil))

	// 字面量：正则元字符按原文匹配
	out := executeTool(t, ts, "search_document", `{"pattern":"3.1.2","literal":true}`)
	if !strings.Contains(out.Output, "1:版本 3.1.2") || strings.Contains(out.Output, "35102") {
		t.Fatalf("literal 输出 = %q", out.Output)
	}
	// 非字面量：. 通配会命中 35102
	out = executeTool(t, ts, "search_document", `{"pattern":"3.1.2"}`)
	if !strings.Contains(out.Output, "35102") {
		t.Fatalf("正则模式应通配: %q", out.Output)
	}
	// 忽略大小写
	out = executeTool(t, ts, "search_document", `{"pattern":"config file","ignore_case":true}`)
	if !strings.Contains(out.Output, "3:Config File") {
		t.Fatalf("ignore_case 输出 = %q", out.Output)
	}
	// 无命中
	out = executeTool(t, ts, "search_document", `{"pattern":"不存在"}`)
	if out.Output != "无命中" {
		t.Fatalf("无命中输出 = %q", out.Output)
	}
	// 非法正则：错误回执并提示 literal
	out = executeTool(t, ts, "search_document", `{"pattern":"["}`)
	if !out.IsError || !strings.Contains(out.Output, "literal") {
		t.Fatalf("非法正则回执 = %+v", out)
	}
}

func TestSearchDocumentLimitNotice(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 30; i++ {
		sb.WriteString("hit\n")
	}
	ts := BuildForRun(runDeps(sb.String(), nil))
	out := executeTool(t, ts, "search_document", `{"pattern":"hit","limit":5}`)
	if !strings.Contains(out.Output, "已达 5 条命中上限。用 limit=10") {
		t.Fatalf("缺命中上限提示: %q", out.Output)
	}
}

/* ---- write_work_items + DraftSink ---- */

func writeArgs(items ...string) string { return `{"items":[` + strings.Join(items, ",") + `]}` }

func TestWriteWorkItemsAccept(t *testing.T) {
	d := runDeps("doc", nil)
	ts := BuildForRun(d)
	out := executeTool(t, ts, "write_work_items", writeArgs(
		`{"project_name":"用户中心","title":"实现登录","priority":"High","estimated_hours":8}`))
	if out.IsError {
		t.Fatalf("合法写入被拒: %+v", out)
	}
	var r struct {
		Accepted     int `json:"accepted"`
		TotalInDraft int `json:"total_in_draft"`
	}
	if err := json.Unmarshal([]byte(out.Output), &r); err != nil || r.Accepted != 1 || r.TotalInDraft != 1 {
		t.Fatalf("回执 = %s (%v)", out.Output, err)
	}
	items := d.Sink.Items()
	if len(items) != 1 || items[0].Title != "实现登录" || items[0].TypeID != "story" {
		t.Fatalf("sink = %+v", items) // NormalizeDraft 默认值应已填充
	}
}

func TestWriteWorkItemsUpsertAndReplace(t *testing.T) {
	d := runDeps("doc", nil)
	ts := BuildForRun(d)

	executeTool(t, ts, "write_work_items", writeArgs(
		`{"project_name":"P","title":"T1","description":"v1"}`))
	out := executeTool(t, ts, "write_work_items", writeArgs(
		`{"project_name":"P","title":"T1","description":"v2"}`)) // 同项目同名 → 覆盖修订
	var r struct {
		Updated int `json:"updated"`
	}
	_ = json.Unmarshal([]byte(out.Output), &r)
	if r.Updated != 1 || d.Sink.Len() != 1 {
		t.Fatalf("同 key 应覆盖: %s, len=%d", out.Output, d.Sink.Len())
	}
	if d.Sink.Items()[0].Description != "v2" {
		t.Fatalf("覆盖未生效: %+v", d.Sink.Items()[0])
	}

	executeTool(t, ts, "write_work_items", writeArgs(`{"project_name":"P","title":"T2"}`))
	if d.Sink.Len() != 2 {
		t.Fatalf("追加失败 len=%d", d.Sink.Len())
	}
	// replace_all：整体重写
	executeTool(t, ts, "write_work_items", `{"replace_all":true,"items":[{"project_name":"Q","title":"only"}]}`)
	if d.Sink.Len() != 1 || d.Sink.Items()[0].ProjectName != "Q" {
		t.Fatalf("replace_all = %+v", d.Sink.Items())
	}
}

func TestWriteWorkItemsRejectFeedback(t *testing.T) {
	d := runDeps("doc", nil)
	ts := BuildForRun(d)

	// 混合批次：一条合法 + 三条非法（空标题/非法优先级/非法工时）
	out := executeTool(t, ts, "write_work_items", writeArgs(
		`{"project_name":"P","title":"ok"}`,
		`{"project_name":"P","title":"  "}`,
		`{"project_name":"P","title":"bad","priority":"Urgent"}`,
		`{"project_name":"P","title":"bad2","estimated_hours":-1}`,
	))
	if out.IsError {
		t.Fatalf("部分拒绝不算整体失败: %+v", out)
	}
	if !strings.Contains(out.Output, `"rejected"`) || !strings.Contains(out.Output, "「标题」") {
		t.Fatalf("回执缺拒绝明细: %s", out.Output)
	}
	if d.Sink.Len() != 1 {
		t.Fatalf("仅合法条目入 sink: %d", d.Sink.Len())
	}
	// 全拒：IsError 回执
	out = executeTool(t, ts, "write_work_items", writeArgs(`{"project_name":"P","title":""}`))
	if !out.IsError {
		t.Fatalf("全拒应 IsError: %+v", out)
	}
}

func TestDraftSinkReplayFrom(t *testing.T) {
	sink := NewDraftSink()
	toolCallAsst := func(id, name, args string) port.Message {
		c := port.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
		return port.Message{Role: port.RoleAssistant,
			Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &c}}}
	}
	msgs := []port.Message{
		port.NewUserMessage("清单"),
		toolCallAsst("c1", "write_work_items", writeArgs(`{"project_name":"P","title":"T1"}`)),
		toolCallAsst("c2", "read_document", `{}`), // 非写入工具：忽略
		toolCallAsst("c3", "write_work_items", writeArgs(
			`{"project_name":"P","title":"T1","description":"v2"}`, // 覆盖
			`{"project_name":"Q","title":"T2"}`)),
		toolCallAsst("c4", "write_work_items", writeArgs(`{"title":""}`)), // 非法：重放同样跳过
	}
	sink.ReplayFrom(msgs)

	if sink.Len() != 2 {
		t.Fatalf("重放条数 = %d", sink.Len())
	}
	items := sink.Items()
	if items[0].Title != "T1" || items[0].Description != "v2" || items[1].ProjectName != "Q" {
		t.Fatalf("重放结果 = %+v", items)
	}
}

/* ---- ask_human ---- */

func TestAskHumanAnswer(t *testing.T) {
	ts := BuildForRun(runDeps("doc", &fakeAsker{answer: "内部"}))
	out := executeTool(t, ts, "ask_human", `{"question":"部署环境？","options":["内部","公有云"]}`)
	if out.IsError || !strings.Contains(out.Output, "人工回答：内部") {
		t.Fatalf("输出 = %+v", out)
	}
}

func TestAskHumanUnavailable(t *testing.T) {
	ts := BuildForRun(runDeps("doc", nil)) // Dialog 未注入
	out := executeTool(t, ts, "ask_human", `{"question":"q"}`)
	if !out.IsError || !strings.Contains(out.Output, "不可用") {
		t.Fatalf("输出 = %+v", out)
	}
}

func TestAskHumanCancelled(t *testing.T) {
	ts := BuildForRun(runDeps("doc", blockingAsker{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan agent.ToolOutput, 1)
	go func() {
		done <- executeToolCtx(t, ts, ctx, "ask_human", `{"question":"q"}`)
	}()
	time.Sleep(50 * time.Millisecond) // 等 Ask 进入阻塞
	cancel()
	select {
	case out := <-done:
		if !out.IsError || !strings.Contains(out.Output, "暂停") {
			t.Fatalf("取消回执 = %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask 未随 ctx 取消返回")
	}
}

/* ---- 提示词贡献（agent.DocumentedTool，系统提示词同源组装的依据） ---- */

func TestToolsCarryPromptContributions(t *testing.T) {
	ts := BuildForRun(runDeps("doc", nil))
	names := map[string]bool{}
	for _, tool := range ts {
		spec := tool.Spec()
		if !json.Valid(spec.Parameters) {
			t.Fatalf("工具 %s 的 Parameters 非法 JSON: %s", spec.Name, spec.Parameters)
		}
		dt, ok := tool.(agent.DocumentedTool)
		if !ok {
			t.Fatalf("工具 %s 未实现提示词贡献", tool.Spec().Name)
		}
		if dt.PromptSnippet() == "" || len(dt.PromptGuidelines()) == 0 {
			t.Fatalf("工具 %s 提示词贡献为空", tool.Spec().Name)
		}
		names[tool.Spec().Name] = true
	}
	for _, want := range []string{"read_document", "search_document", "write_work_items", "ask_human"} {
		if !names[want] {
			t.Fatalf("缺少工具 %s", want)
		}
	}
}
