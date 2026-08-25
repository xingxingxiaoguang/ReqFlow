// Package llm 实现 port.LLMClient：OpenAI 兼容 /chat/completions（DeepSeek/GLM/Qwen/Kimi 等适用）。
// 流式输出区分思考与正文两相位（reasoning_content / content），
// 思考内容只透传展示、不计入正文返回值，避免污染结构化解析。
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
	"time"

	"reqflow/internal/port"
)

// Options 客户端参数（由 cmd 从配置注入）。
type Options struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

// Client OpenAI 兼容对话客户端。
type Client struct {
	opt  Options
	http *http.Client
}

// New 构造客户端；apiKey 为空时仍可构造，调用将返回明确错误（功能降级由 app 判断）。
func New(opt Options) *Client {
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Minute
	}
	return &Client{opt: opt, http: &http.Client{Timeout: opt.Timeout}}
}

func (c *Client) available() error {
	if c.opt.APIKey == "" || c.opt.BaseURL == "" || c.opt.Model == "" {
		return fmt.Errorf("LLM 未配置（base_url/api_key/model），请在 config.yaml 填写后重启")
	}
	return nil
}

func (c *Client) requestBody(prompt string, stream bool) map[string]any {
	return map[string]any{
		"model": c.opt.Model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"stream":      stream,
		"temperature": c.opt.Temperature,
		"max_tokens":  c.opt.MaxTokens,
	}
}

func (c *Client) newRequest(ctx context.Context, stream bool, prompt string) (*http.Request, error) {
	buf, err := json.Marshal(c.requestBody(prompt, stream))
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

// StreamChat 流式对话。返回拼接后的完整正文（不含思考内容）。
func (c *Client) StreamChat(ctx context.Context, prompt string, onDelta func(port.StreamDelta)) (string, error) {
	if err := c.available(); err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, true, prompt)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM 流式请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var answer strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	// 单行可能极大（长 JSON 增量），放大缓冲
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				break
			}
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // 容忍偶发非 JSON 心跳行
		}
		for _, ch := range chunk.Choices {
			if r := ch.Delta.ReasoningContent; r != "" {
				if onDelta != nil {
					onDelta(port.StreamDelta{Phase: port.PhaseThinking, Text: r})
				}
			} else if r := ch.Delta.Reasoning; r != "" {
				if onDelta != nil {
					onDelta(port.StreamDelta{Phase: port.PhaseThinking, Text: r})
				}
			}
			if s := ch.Delta.Content; s != "" {
				answer.WriteString(s)
				if onDelta != nil {
					onDelta(port.StreamDelta{Phase: port.PhaseAnswer, Text: s})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return answer.String(), fmt.Errorf("LLM 流读取中断: %w", err)
	}
	return answer.String(), nil
}

// Chat 非流式对话（流式解析失败时的回退通道）。
func (c *Client) Chat(ctx context.Context, prompt string) (string, error) {
	if err := c.available(); err != nil {
		return "", err
	}
	req, err := c.newRequest(ctx, false, prompt)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("LLM HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("LLM 响应异常: %s", truncate(string(body), 300))
	}
	return out.Choices[0].Message.Content, nil
}

// Ping 连通性测试。
func (c *Client) Ping(ctx context.Context) error {
	if err := c.available(); err != nil {
		return err
	}
	_, err := c.Chat(ctx, "Hi")
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
