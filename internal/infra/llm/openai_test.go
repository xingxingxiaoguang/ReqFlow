package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reqflow/internal/port"
)

func testOptions(baseURL string) Options {
	return Options{
		Provider:    ProviderOpenAI,
		BaseURL:     baseURL,
		APIKey:      "test-key",
		Model:       "test-model",
		Temperature: 0.7,
		MaxTokens:   1024,
	}
}

// sseHandler 逐段写入 SSE data 行。
func sseHandler(t *testing.T, datas ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, d := range datas {
			_, _ = w.Write([]byte("data: " + d + "\n\n"))
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
}

func collectEvents(onEvent func(port.AssistantEvent)) (func(port.AssistantEvent), *[]port.EventType) {
	types := &[]port.EventType{}
	return func(ev port.AssistantEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
		*types = append(*types, ev.Type)
	}, types
}

func TestOpenAIStreamPhases(t *testing.T) {
	srv := sseHandler(t,
		`{"choices":[{"delta":{"reasoning_content":"思考片段"}}]}`,
		`{"choices":[{"delta":{"content":"[{\"title\":\"A\"}"}}]}`,
		`{"choices":[{"delta":{"content":"]"}},{"finish_reason":"stop"}]}`,
	)
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}

	var phases []string
	var deltas []string
	msg, err := c.Stream(context.Background(), &port.Context{Messages: []port.Message{port.NewUserMessage("hi")}},
		func(ev port.AssistantEvent) {
			switch ev.Type {
			case port.EventThinkingDelta:
				phases = append(phases, "thinking")
				deltas = append(deltas, ev.Delta)
			case port.EventTextDelta:
				phases = append(phases, "answer")
				deltas = append(deltas, ev.Delta)
			}
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if want := `[{"title":"A"}]`; msg.Text() != want {
		t.Fatalf("Text() = %q, want %q", msg.Text(), want)
	}
	// 思考内容只进 thinking 块，不混入 Text()
	if got := msg.Content[0].Thinking; got != "思考片段" {
		t.Fatalf("thinking block = %q", got)
	}
	if strings.Join(phases, ",") != "thinking,answer,answer" {
		t.Fatalf("phases = %v", phases)
	}
	if msg.StopReason != port.StopReasonStop {
		t.Fatalf("StopReason = %s", msg.StopReason)
	}
}

func TestOpenAIStreamToolCallAggregation(t *testing.T) {
	// 同一 index 两次增量拆开 JSON 参数；再出现第二个工具调用
	srv := sseHandler(t,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"search","arguments":"{\"q\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"登录\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call-2","function":{"name":"create","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}

	var toolEnds int
	msg, err := c.Stream(context.Background(), &port.Context{}, func(ev port.AssistantEvent) {
		if ev.Type == port.EventToolCallEnd {
			toolEnds++
		}
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	calls := msg.ToolCalls()
	if len(calls) != 2 {
		t.Fatalf("ToolCalls = %d, want 2", len(calls))
	}
	if calls[0].ID != "call-1" || calls[0].Name != "search" {
		t.Fatalf("calls[0] = %+v", calls[0])
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil || args["q"] != "登录" {
		t.Fatalf("calls[0].Arguments = %s (%v)", calls[0].Arguments, err)
	}
	if msg.StopReason != port.StopReasonToolUse {
		t.Fatalf("StopReason = %s", msg.StopReason)
	}
	if toolEnds != 2 {
		t.Fatalf("toolcall_end 事件数 = %d", toolEnds)
	}
}

func TestOpenAIStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}
	_, err := c.Stream(context.Background(), &port.Context{}, nil)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want HTTP 500 error, got %v", err)
	}
}

func TestOpenAIStreamMissingFinishReasonInferred(t *testing.T) {
	// 杂牌端点不发 finish_reason：有工具调用应推断 toolUse
	srv := sseHandler(t,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":"{}"}}]}}]}`,
	)
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}
	msg, err := c.Stream(context.Background(), &port.Context{}, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if msg.StopReason != port.StopReasonToolUse {
		t.Fatalf("StopReason = %s, want toolUse", msg.StopReason)
	}
}

func TestOpenAIComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"content":          "正文",
					"reasoning_content": "推理",
					"tool_calls": []map[string]any{{
						"id": "c1", "type": "function",
						"function": map[string]any{"name": "f", "arguments": `{"x":1}`},
					}},
				},
			}},
		})
	}))
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}
	msg, err := c.Complete(context.Background(), &port.Context{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if msg.Text() != "正文" || msg.Content[0].Thinking != "推理" {
		t.Fatalf("msg = %+v", msg.Content)
	}
	if len(msg.ToolCalls()) != 1 || msg.ToolCalls()[0].Name != "f" {
		t.Fatalf("ToolCalls = %+v", msg.ToolCalls())
	}
	if msg.StopReason != port.StopReasonToolUse {
		t.Fatalf("StopReason = %s", msg.StopReason)
	}
}

func TestOpenAIReplayRendersContext(t *testing.T) {
	// assistant(文本+工具调用) + toolResult 的回放格式
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"finish_reason": "stop", "message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()
	c := &openaiClient{opt: testOptions(srv.URL), http: srv.Client()}

	call := port.ToolCall{ID: "c1", Name: "search", Arguments: json.RawMessage(`{"q":"x"}`)}
	cc := &port.Context{
		SystemPrompt: "系统提示",
		Messages: []port.Message{
			port.NewUserMessage("问题"),
			{Role: port.RoleAssistant, Content: []port.Block{
				{Type: port.BlockText, Text: "我先查一下"},
				{Type: port.BlockToolCall, ToolCall: &call},
			}},
			port.NewToolResultMessage(call, "查到了", "展示用", false),
		},
		Tools: []port.ToolSpec{{Name: "search", Description: "搜索", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	if _, err := c.Complete(context.Background(), cc); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 4 { // system + user + assistant + tool
		t.Fatalf("messages = %d, want 4", len(msgs))
	}
	assistant := msgs[2].(map[string]any)
	if assistant["content"] != "我先查一下" {
		t.Fatalf("assistant content = %v", assistant["content"])
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "c1" || toolMsg["content"] != "查到了" {
		t.Fatalf("tool msg = %v", toolMsg)
	}
	if gotBody["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", gotBody["tool_choice"])
	}
}
