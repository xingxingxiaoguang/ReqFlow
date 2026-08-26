package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reqflow/internal/port"
)

// anthropicSSE 按事件名+data 写 SSE 帧。
func anthropicSSE(t *testing.T, frames ...[2]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "your-test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Error("缺少 anthropic-version 头")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, f := range frames {
			_, _ = w.Write([]byte("event: " + f[0] + "\ndata: " + f[1] + "\n\n"))
			flusher.Flush()
		}
	}))
}

func anthropicOptions(baseURL string) Options {
	return Options{Provider: ProviderAnthropic, BaseURL: baseURL, APIKey: "your-test-key", Model: "claude-test", MaxTokens: 512}
}

func TestAnthropicStreamFullSequence(t *testing.T) {
	srv := anthropicSSE(t,
		[2]string{"message_start", `{"message":{"usage":{"input_tokens":10,"output_tokens":2}}}`},
		[2]string{"content_block_start", `{"index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`},
		[2]string{"content_block_delta", `{"index":0,"delta":{"type":"thinking_delta","thinking":"推理中"}}`},
		[2]string{"content_block_delta", `{"index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`},
		[2]string{"content_block_stop", `{"index":0}`},
		[2]string{"content_block_start", `{"index":1,"content_block":{"type":"text","text":""}}`},
		[2]string{"content_block_delta", `{"index":1,"delta":{"type":"text_delta","text":"[{\"title\":\"B\"}]"}}`},
		[2]string{"content_block_stop", `{"index":1}`},
		[2]string{"content_block_start", `{"index":2,"content_block":{"type":"tool_use","id":"tu-1","name":"search","input":{}}}`},
		[2]string{"content_block_delta", `{"index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`},
		[2]string{"content_block_delta", `{"index":2,"delta":{"type":"input_json_delta","partial_json":"\"x\"}"}}`},
		[2]string{"content_block_stop", `{"index":2}`},
		[2]string{"message_delta", `{"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}`},
		[2]string{"message_stop", `{}`},
	)
	defer srv.Close()
	c := &anthropicClient{opt: anthropicOptions(srv.URL), http: srv.Client()}

	var events []port.EventType
	msg, err := c.Stream(context.Background(), &port.Context{}, func(ev port.AssistantEvent) {
		events = append(events, ev.Type)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if msg.Text() != `[{"title":"B"}]` {
		t.Fatalf("Text() = %q", msg.Text())
	}
	if msg.Content[0].Type != port.BlockThinking || msg.Content[0].Thinking != "推理中" {
		t.Fatalf("thinking block = %+v", msg.Content[0])
	}
	if msg.Content[0].ThinkingSignature != "sig-abc" {
		t.Fatalf("signature = %q（回放必需）", msg.Content[0].ThinkingSignature)
	}
	calls := msg.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "tu-1" || calls[0].Name != "search" {
		t.Fatalf("ToolCalls = %+v", calls)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil || args["q"] != "x" {
		t.Fatalf("args = %s (%v)", calls[0].Arguments, err)
	}
	if msg.StopReason != port.StopReasonToolUse {
		t.Fatalf("StopReason = %s", msg.StopReason)
	}
	if msg.Usage.Input != 10 || msg.Usage.Output != 30 {
		t.Fatalf("Usage = %+v", msg.Usage)
	}
	if !containsEvent(events, port.EventToolCallEnd) || !containsEvent(events, port.EventThinkingDelta) {
		t.Fatalf("events = %v", events)
	}
}

func containsEvent(events []port.EventType, want port.EventType) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

func TestAnthropicComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "thinking", "thinking": "想", "signature": "s"},
				{"type": "text", "text": "答案"},
			},
			"usage": map[string]any{"input_tokens": 5, "output_tokens": 6},
		})
	}))
	defer srv.Close()
	c := &anthropicClient{opt: anthropicOptions(srv.URL), http: srv.Client()}
	msg, err := c.Complete(context.Background(), &port.Context{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if msg.Text() != "答案" || msg.StopReason != port.StopReasonStop {
		t.Fatalf("msg.Text=%q stop=%s", msg.Text(), msg.StopReason)
	}
}

func TestAnthropicURLNormalization(t *testing.T) {
	c := &anthropicClient{opt: anthropicOptions("https://api.anthropic.com/")}
	if got := c.messagesURL(); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("url = %q", got)
	}
	c2 := &anthropicClient{opt: anthropicOptions("https://x.example/v1")}
	if got := c2.messagesURL(); got != "https://x.example/v1/messages" {
		t.Fatalf("url = %q", got)
	}
}

func TestAnthropicReplayGroupsToolResults(t *testing.T) {
	// 连续两条 toolResult 必须合并为一条 user 消息的 tool_result 块；assistant 回放带签名
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
		})
	}))
	defer srv.Close()
	c := &anthropicClient{opt: anthropicOptions(srv.URL), http: srv.Client()}

	call1 := port.ToolCall{ID: "t1", Name: "a", Arguments: json.RawMessage("{}")}
	call2 := port.ToolCall{ID: "t2", Name: "b", Arguments: json.RawMessage("{}")}
	cc := &port.Context{
		SystemPrompt: "sys",
		Messages: []port.Message{
			port.NewUserMessage("问题"),
			{Role: port.RoleAssistant, Content: []port.Block{
				{Type: port.BlockThinking, Thinking: "想", ThinkingSignature: "sig"},
				{Type: port.BlockToolCall, ToolCall: &call1},
				{Type: port.BlockToolCall, ToolCall: &call2},
			}},
			port.NewToolResultMessage(call1, "结果一", "", false),
			port.NewToolResultMessage(call2, "", "", true), // 空结果回执占位 + is_error
		},
	}
	if _, err := c.Complete(context.Background(), cc); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["system"] != "sys" {
		t.Fatalf("system = %v", body["system"])
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 { // user + assistant + 合并后的 tool_result user
		t.Fatalf("messages = %d, want 3", len(msgs))
	}
	assistant := msgs[1].(map[string]any)
	blocks := assistant["content"].([]any)
	thinking := blocks[0].(map[string]any)
	if thinking["signature"] != "sig" {
		t.Fatalf("thinking 签名未回放: %v", thinking)
	}
	toolUser := msgs[2].(map[string]any)
	if toolUser["role"] != "user" {
		t.Fatalf("tool_result 应在 user 消息内, role=%v", toolUser["role"])
	}
	trBlocks := toolUser["content"].([]any)
	if len(trBlocks) != 2 {
		t.Fatalf("tool_result 块 = %d, want 2（合并）", len(trBlocks))
	}
	if trBlocks[1].(map[string]any)["is_error"] != true {
		t.Fatalf("is_error 未回传: %v", trBlocks[1])
	}
}

func TestAnthropicStreamError(t *testing.T) {
	srv := anthropicSSE(t,
		[2]string{"error", `{"error":{"message":"overloaded"}}`},
	)
	defer srv.Close()
	c := &anthropicClient{opt: anthropicOptions(srv.URL), http: srv.Client()}
	msg, err := c.Stream(context.Background(), &port.Context{}, nil)
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("want error, got %v", err)
	}
	if msg.StopReason != port.StopReasonError {
		t.Fatalf("StopReason = %s", msg.StopReason)
	}
}
