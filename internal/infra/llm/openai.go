// Package llm 实现 port.LLMClient。
// openai.go：OpenAI 兼容 /chat/completions 适配器，核心逻辑移植自 pi
// （https://github.com/earendil-works/pi，MIT License, Copyright (c) 2025 Mario Zechner）
// 的 packages/ai/src/api/openai-completions.ts 子集：
//   - reasoning 三字段（reasoning_content / reasoning / reasoning_text，取首个非空防重复）
//   - tool_calls 流式增量按 index 聚合参数
//   - finish_reason 映射；流结束缺 finish_reason 时按是否有工具调用推断（宽松偏离）
//   - assistant 回放为纯文本字符串（部分端点会镜像内容块结构导致递归嵌套）
// 不移植：厂商 compat 矩阵、模型注册表、会话亲和头、自定义 grammar 工具。
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

type openaiClient struct {
	opt  Options
	http *http.Client
}

/* ---- 请求渲染（Context → OpenAI 兼容格式，pi convertMessages 子集） ---- */

type apiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (c *openaiClient) buildMessages(cc *port.Context) []map[string]any {
	var out []map[string]any
	if cc.SystemPrompt != "" {
		out = append(out, map[string]any{"role": "system", "content": cc.SystemPrompt})
	}
	for i := range cc.Messages {
		msg := &cc.Messages[i]
		switch msg.Role {
		case port.RoleUser:
			var sb strings.Builder
			for j := range msg.Content {
				if msg.Content[j].Type == port.BlockText {
					sb.WriteString(msg.Content[j].Text)
				}
			}
			out = append(out, map[string]any{"role": "user", "content": sb.String()})
		case port.RoleAssistant:
			text := msg.Text()
			m := map[string]any{"role": "assistant"}
			if text != "" {
				m["content"] = text
			}
			calls := msg.ToolCalls()
			if len(calls) > 0 {
				arr := make([]apiToolCall, 0, len(calls))
				for _, tc := range calls {
					var ac apiToolCall
					ac.ID = tc.ID
					ac.Type = "function"
					ac.Function.Name = tc.Name
					if len(tc.Arguments) == 0 {
						ac.Function.Arguments = "{}"
					} else {
						ac.Function.Arguments = string(tc.Arguments)
					}
					arr = append(arr, ac)
				}
				m["tool_calls"] = arr
			}
			// 部分端点要求 content 与 tool_calls 至少其一；两者皆空的 assistant 跳过
			if text == "" && len(calls) == 0 {
				continue
			}
			out = append(out, m)
		case port.RoleToolResult:
			content := msg.Result
			if strings.TrimSpace(content) == "" {
				content = "(no tool output)"
			}
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      content,
			})
		}
	}
	return out
}

func (c *openaiClient) buildBody(cc *port.Context, stream bool) map[string]any {
	body := map[string]any{
		"model":       c.opt.Model,
		"messages":    c.buildMessages(cc),
		"stream":      stream,
		"temperature": c.opt.Temperature,
	}
	if c.opt.MaxTokens > 0 {
		body["max_tokens"] = c.opt.MaxTokens
	}
	if len(cc.Tools) > 0 {
		tools := make([]map[string]any, 0, len(cc.Tools))
		for _, t := range cc.Tools {
			params := json.RawMessage("{}")
			if len(t.Parameters) > 0 {
				params = t.Parameters
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  params,
				},
			})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	return body
}

func (c *openaiClient) newRequest(ctx context.Context, cc *port.Context, stream bool) (*http.Request, error) {
	buf, err := json.Marshal(c.buildBody(cc, stream))
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.opt.BaseURL, "/")+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.opt.APIKey)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return req, nil
}

/* ---- 流式（pi 流解析子集） ---- */

// toolCallAgg 流式工具调用聚合缓冲（pi ensureToolCallBlock 的对应物）。
type toolCallAgg struct {
	order  int
	id     string
	name   string
	args   strings.Builder
}

// mapFinishReason pi mapStopReason 子集。
func mapFinishReason(reason string) (port.StopReason, string) {
	switch reason {
	case "stop":
		return port.StopReasonStop, ""
	case "length":
		return port.StopReasonLength, ""
	case "tool_calls", "function_call":
		return port.StopReasonToolUse, ""
	case "content_filter":
		return port.StopReasonError, "内容被安全过滤拦截"
	default:
		return port.StopReasonStop, ""
	}
}

func (c *openaiClient) Stream(ctx context.Context, cc *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
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

	var (
		hasFinish bool
		toolAggs  = map[int]*toolCallAgg{}
		nextBlock int // 内容块序号（事件 ContentIndex）
	)
	ensureBlock := func(t port.BlockType) *port.Block {
		// 相邻同类型增量合并进同一块（pi 的 ensureTextBlock/ensureThinkingBlock 行为）
		if n := len(out.Content); n > 0 && out.Content[n-1].Type == t {
			return &out.Content[n-1]
		}
		out.Content = append(out.Content, port.Block{Type: t})
		nextBlock = len(out.Content) - 1
		return &out.Content[len(out.Content)-1]
	}

	scanner := bufio.NewScanner(resp.Body)
	// 单行可能极大（长 JSON 增量），放大缓冲
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ReasoningText    string `json:"reasoning_text"`
					ToolCalls        []struct {
						Index    *int   `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 容忍偶发非 JSON 心跳行
		}
		if chunk.Usage != nil {
			out.Usage.Input = chunk.Usage.PromptTokens
			out.Usage.Output = chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]

		if ch.FinishReason != nil && *ch.FinishReason != "" {
			out.StopReason, out.ErrorMessage = mapFinishReason(*ch.FinishReason)
			hasFinish = true
		}

		d := ch.Delta
		if d.Content != "" {
			b := ensureBlock(port.BlockText)
			b.Text += d.Content
			emit(port.AssistantEvent{Type: port.EventTextDelta, Delta: d.Content, ContentIndex: nextBlock, Message: out})
		}
		// pi：三字段取首个非空，防部分端点重复返回相同内容
		for _, r := range []string{d.ReasoningContent, d.Reasoning, d.ReasoningText} {
			if r != "" {
				b := ensureBlock(port.BlockThinking)
				b.Thinking += r
				emit(port.AssistantEvent{Type: port.EventThinkingDelta, Delta: r, ContentIndex: nextBlock, Message: out})
				break
			}
		}
		for _, tc := range d.ToolCalls {
			idx := 0
			if tc.Index != nil {
				idx = *tc.Index
			}
			agg, ok := toolAggs[idx]
			if !ok {
				agg = &toolCallAgg{order: len(toolAggs)}
				toolAggs[idx] = agg
			}
			if tc.ID != "" {
				agg.id = tc.ID
			}
			if tc.Function.Name != "" {
				agg.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				agg.args.WriteString(tc.Function.Arguments)
				emit(port.AssistantEvent{Type: port.EventToolCallDelta, Delta: tc.Function.Arguments, Message: out})
			}
		}
	}
	scanErr := scanner.Err()

	// 聚合完成的工具调用 → 内容块（按到达顺序）
	if len(toolAggs) > 0 {
		ordered := make([]*toolCallAgg, 0, len(toolAggs))
		for _, agg := range toolAggs {
			ordered = append(ordered, agg)
		}
		for i := 1; i < len(ordered); i++ {
			for j := i; j > 0 && ordered[j].order < ordered[j-1].order; j-- {
				ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
			}
		}
		for _, agg := range ordered {
			args := json.RawMessage(agg.args.String())
			if !json.Valid(args) {
				args = json.RawMessage("{}")
			}
			call := port.ToolCall{ID: agg.id, Name: agg.name, Arguments: args}
			out.Content = append(out.Content, port.Block{Type: port.BlockToolCall, ToolCall: &call})
			emit(port.AssistantEvent{Type: port.EventToolCallEnd, ContentIndex: len(out.Content) - 1, ToolCall: &call, Message: out})
		}
	}

	// 中止：ctx 取消优先于一切
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
	// 缺 finish_reason 的端点按是否产生工具调用推断（宽松偏离，兼容杂牌兼容端点）
	if !hasFinish {
		if len(toolAggs) > 0 {
			out.StopReason = port.StopReasonToolUse
		} else {
			out.StopReason = port.StopReasonStop
		}
	}
	emit(port.AssistantEvent{Type: port.EventDone, Message: out})
	return out, nil
}

/* ---- 非流式 ---- */

func (c *openaiClient) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
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
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []apiToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Choices) == 0 {
		return out, fmt.Errorf("LLM 响应异常: %s", truncate(string(body), 300))
	}
	if parsed.Usage != nil {
		out.Usage.Input = parsed.Usage.PromptTokens
		out.Usage.Output = parsed.Usage.CompletionTokens
	}
	ch := parsed.Choices[0]
	if r := firstNonEmpty(ch.Message.ReasoningContent, ch.Message.Reasoning); r != "" {
		out.Content = append(out.Content, port.Block{Type: port.BlockThinking, Thinking: r})
	}
	if ch.Message.Content != "" {
		out.Content = append(out.Content, port.Block{Type: port.BlockText, Text: ch.Message.Content})
	}
	for _, tc := range ch.Message.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		call := port.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args}
		out.Content = append(out.Content, port.Block{Type: port.BlockToolCall, ToolCall: &call})
	}
	if len(ch.FinishReason) > 0 {
		out.StopReason, out.ErrorMessage = mapFinishReason(ch.FinishReason)
	} else {
		out.StopReason = port.StopReasonStop
	}
	return out, nil
}

func (c *openaiClient) Ping(ctx context.Context) error {
	if err := c.available(); err != nil {
		return err
	}
	cc := &port.Context{Messages: []port.Message{port.NewUserMessage("Hi")}}
	_, err := c.Complete(ctx, cc)
	return err
}
