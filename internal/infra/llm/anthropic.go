// anthropic.go：Anthropic Messages 协议适配器，移植自 pi
// （https://github.com/earendil-works/pi，MIT License, Copyright (c) 2025 Mario Zechner）
// 的 packages/ai/src/api/anthropic-messages.ts 子集：
//   - system 顶层抽取；SSE 事件状态机（message_start / content_block_* / message_delta）
//   - thinking 块含签名（回放必须原样带回）；tool_use 流式 input_json_delta 聚合
//   - 连续 toolResult 合并为单条 user 消息的 tool_result 块（协议要求）
//
// 不移植：cache_control 提示缓存、fallbacks、fine-grained tool streaming beta 头。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"reqflow/internal/port"
)

type anthropicClient struct {
	opt  Options
	http *http.Client
}

// messagesURL 归一化 base_url 到 /v1/messages（容忍用户配到域名根或已带 /v1）。
func (c *anthropicClient) messagesURL() string {
	base := strings.TrimSuffix(c.opt.BaseURL, "/")
	base = strings.TrimSuffix(base, "/messages")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/messages"
}

func (c *anthropicClient) newRequest(ctx context.Context, cc *port.Context, stream bool) (*http.Request, error) {
	body := map[string]any{
		"model":      c.opt.Model,
		"max_tokens": c.opt.MaxTokens,
		"stream":     stream,
	}
	if c.opt.MaxTokens <= 0 {
		body["max_tokens"] = 4096 // Anthropic 必填
	}
	if c.opt.Temperature > 0 {
		body["temperature"] = c.opt.Temperature
	}
	if cc.SystemPrompt != "" {
		body["system"] = cc.SystemPrompt
	}
	if len(cc.Tools) > 0 {
		tools := make([]map[string]any, 0, len(cc.Tools))
		for _, t := range cc.Tools {
			params := json.RawMessage("{}")
			if len(t.Parameters) > 0 {
				params = t.Parameters
			}
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": params,
			})
		}
		body["tools"] = tools
	}
	body["messages"] = c.buildMessages(cc)

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.messagesURL(), bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.opt.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

// buildMessages Context → Messages 协议。assistant 回放 thinking（带签名）与 tool_use；
// 连续 toolResult 合并为一条 user 消息（协议要求 tool_result 必须紧跟其后单条回传）。
func (c *anthropicClient) buildMessages(cc *port.Context) []map[string]any {
	var out []map[string]any
	flushToolResults := func(batch []port.Message) {
		blocks := make([]map[string]any, 0, len(batch))
		for i := range batch {
			m := &batch[i]
			content := m.Result
			if strings.TrimSpace(content) == "" {
				content = "(no tool output)"
			}
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     content,
				"is_error":    m.IsError,
			})
		}
		out = append(out, map[string]any{"role": "user", "content": blocks})
	}

	var pendingTools []port.Message
	for i := range cc.Messages {
		msg := &cc.Messages[i]
		if msg.Role == port.RoleToolResult {
			pendingTools = append(pendingTools, *msg)
			continue
		}
		if len(pendingTools) > 0 {
			flushToolResults(pendingTools)
			pendingTools = nil
		}
		switch msg.Role {
		case port.RoleUser:
			out = append(out, map[string]any{"role": "user", "content": msg.Text()})
		case port.RoleAssistant:
			blocks := make([]map[string]any, 0, len(msg.Content))
			for j := range msg.Content {
				b := &msg.Content[j]
				switch b.Type {
				case port.BlockText:
					blocks = append(blocks, map[string]any{"type": "text", "text": b.Text})
				case port.BlockThinking:
					// 回放 thinking 必须带 signature（扩展思考 + 工具调用场景 API 强校验）
					blocks = append(blocks, map[string]any{
						"type":      "thinking",
						"thinking":  b.Thinking,
						"signature": b.ThinkingSignature,
					})
				case port.BlockToolCall:
					args := json.RawMessage("{}")
					if len(b.ToolCall.Arguments) > 0 {
						args = b.ToolCall.Arguments
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    b.ToolCall.ID,
						"name":  b.ToolCall.Name,
						"input": args,
					})
				}
			}
			if len(blocks) > 0 {
				out = append(out, map[string]any{"role": "assistant", "content": blocks})
			}
		}
	}
	if len(pendingTools) > 0 {
		flushToolResults(pendingTools)
	}
	return out
}

// mapAnthropicStopReason pi mapStopReason 子集。
func mapAnthropicStopReason(reason string) (port.StopReason, string) {
	switch reason {
	case "end_turn", "stop_sequence":
		return port.StopReasonStop, ""
	case "max_tokens":
		return port.StopReasonLength, ""
	case "tool_use":
		return port.StopReasonToolUse, ""
	case "refusal":
		return port.StopReasonError, "模型拒绝响应"
	default:
		return port.StopReasonStop, ""
	}
}

func (c *anthropicClient) Stream(ctx context.Context, cc *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
	if err := c.available(); err != nil {
		return nil, err
	}
	emit := func(ev port.AssistantEvent) {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	out := &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonPending, Timestamp: timeNowMilli()}
	req, err := c.newRequest(ctx, cc, true)
	if err != nil {
		return out, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("LLM 流式请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return out, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	emit(port.AssistantEvent{Type: port.EventStart, Message: out})

	// Anthropic SSE：event 行命名，data 行携带 JSON；块按协议 index 追踪
	type tracked struct {
		blockIdx int // 我们的 Content 下标；-1 表示尚未建块
		kind     port.BlockType
		jsonBuf  strings.Builder
	}
	blocks := map[int]*tracked{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var eventName string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			eventName = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}

		switch eventName {
		case "message_start":
			var ev struct {
				Message struct {
					Usage struct {
						InputTokens     int `json:"input_tokens"`
						OutputTokens    int `json:"output_tokens"`
						CacheReadInput  int `json:"cache_read_input_tokens"`
						CacheCreationIn int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				out.Usage.Input = ev.Message.Usage.InputTokens
				out.Usage.Output = ev.Message.Usage.OutputTokens
				out.Usage.CacheRead = ev.Message.Usage.CacheReadInput
				out.Usage.CacheWrite = ev.Message.Usage.CacheCreationIn
			}
		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type      string `json:"type"`
					Text      string `json:"text"`
					Thinking  string `json:"thinking"`
					Signature string `json:"signature"`
					ID        string `json:"id"`
					Name      string `json:"name"`
				} `json:"content_block"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			switch ev.ContentBlock.Type {
			case "text":
				out.Content = append(out.Content, port.Block{Type: port.BlockText, Text: ev.ContentBlock.Text})
				blocks[ev.Index] = &tracked{blockIdx: len(out.Content) - 1, kind: port.BlockText}
			case "thinking", "redacted_thinking":
				thinking := ev.ContentBlock.Thinking
				if ev.ContentBlock.Type == "redacted_thinking" {
					thinking = "[Reasoning redacted]"
				}
				out.Content = append(out.Content, port.Block{Type: port.BlockThinking, Thinking: thinking, ThinkingSignature: ev.ContentBlock.Signature})
				blocks[ev.Index] = &tracked{blockIdx: len(out.Content) - 1, kind: port.BlockThinking}
			case "tool_use":
				out.Content = append(out.Content, port.Block{Type: port.BlockToolCall, ToolCall: &port.ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}})
				blocks[ev.Index] = &tracked{blockIdx: len(out.Content) - 1, kind: port.BlockToolCall}
			}
		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					Signature   string `json:"signature"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			t, ok := blocks[ev.Index]
			if !ok || t.blockIdx < 0 || t.blockIdx >= len(out.Content) {
				continue
			}
			b := &out.Content[t.blockIdx]
			switch ev.Delta.Type {
			case "text_delta":
				b.Text += ev.Delta.Text
				emit(port.AssistantEvent{Type: port.EventTextDelta, Delta: ev.Delta.Text, ContentIndex: t.blockIdx, Message: out})
			case "thinking_delta":
				b.Thinking += ev.Delta.Thinking
				emit(port.AssistantEvent{Type: port.EventThinkingDelta, Delta: ev.Delta.Thinking, ContentIndex: t.blockIdx, Message: out})
			case "signature_delta":
				b.ThinkingSignature += ev.Delta.Signature
			case "input_json_delta":
				t.jsonBuf.WriteString(ev.Delta.PartialJSON)
				emit(port.AssistantEvent{Type: port.EventToolCallDelta, Delta: ev.Delta.PartialJSON, ContentIndex: t.blockIdx, Message: out})
			}
		case "content_block_stop":
			var ev struct {
				Index int `json:"index"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if t, ok := blocks[ev.Index]; ok && t.kind == port.BlockToolCall && t.blockIdx < len(out.Content) {
				args := json.RawMessage(t.jsonBuf.String())
				if len(args) == 0 || !json.Valid(args) {
					args = json.RawMessage("{}")
				}
				out.Content[t.blockIdx].ToolCall.Arguments = args
				emit(port.AssistantEvent{Type: port.EventToolCallEnd, ContentIndex: t.blockIdx, ToolCall: out.Content[t.blockIdx].ToolCall, Message: out})
			}
		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage *struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if ev.Delta.StopReason != "" {
				out.StopReason, out.ErrorMessage = mapAnthropicStopReason(ev.Delta.StopReason)
			}
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				out.Usage.Output = ev.Usage.OutputTokens
			}
		case "message_stop":
			// 终止信号，循环自然结束
		case "error":
			var ev struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal([]byte(data), &ev)
			out.StopReason = port.StopReasonError
			out.ErrorMessage = ev.Error.Message
			emit(port.AssistantEvent{Type: port.EventError, Message: out})
			return out, fmt.Errorf("LLM 流式错误: %s", ev.Error.Message)
		}
	}
	scanErr := scanner.Err()

	if ctx.Err() != nil {
		out.StopReason = port.StopReasonAborted
		emit(port.AssistantEvent{Type: port.EventError, Message: out})
		return out, fmt.Errorf("LLM 流已中止: %w", ctx.Err())
	}
	if scanErr != nil {
		out.StopReason = port.StopReasonError
		out.ErrorMessage = scanErr.Error()
		emit(port.AssistantEvent{Type: port.EventError, Message: out})
		return out, fmt.Errorf("LLM 流读取中断: %w", scanErr)
	}
	if out.StopReason == port.StopReasonPending {
		out.StopReason = port.StopReasonStop
	}
	emit(port.AssistantEvent{Type: port.EventDone, Message: out})
	return out, nil
}

func (c *anthropicClient) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	if err := c.available(); err != nil {
		return nil, err
	}
	out := &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonPending, Timestamp: timeNowMilli()}
	req, err := c.newRequest(ctx, cc, false)
	if err != nil {
		return out, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("LLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return out, err
	}
	if resp.StatusCode >= 400 {
		return out, fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var parsed struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Thinking  string          `json:"thinking"`
			Signature string          `json:"signature"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return out, fmt.Errorf("LLM 响应异常: %s", truncate(string(body), 300))
	}
	for _, b := range parsed.Content {
		switch b.Type {
		case "text":
			out.Content = append(out.Content, port.Block{Type: port.BlockText, Text: b.Text})
		case "thinking", "redacted_thinking":
			thinking := b.Thinking
			if b.Type == "redacted_thinking" {
				thinking = "[Reasoning redacted]"
			}
			out.Content = append(out.Content, port.Block{Type: port.BlockThinking, Thinking: thinking, ThinkingSignature: b.Signature})
		case "tool_use":
			args := b.Input
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			call := port.ToolCall{ID: b.ID, Name: b.Name, Arguments: args}
			out.Content = append(out.Content, port.Block{Type: port.BlockToolCall, ToolCall: &call})
		}
	}
	out.Usage.Input = parsed.Usage.InputTokens
	out.Usage.Output = parsed.Usage.OutputTokens
	if parsed.StopReason != "" {
		out.StopReason, out.ErrorMessage = mapAnthropicStopReason(parsed.StopReason)
	} else {
		out.StopReason = port.StopReasonStop
	}
	return out, nil
}

func (c *anthropicClient) Ping(ctx context.Context) error {
	if err := c.available(); err != nil {
		return err
	}
	cc := &port.Context{Messages: []port.Message{port.NewUserMessage("Hi")}}
	_, err := c.Complete(ctx, cc)
	return err
}
