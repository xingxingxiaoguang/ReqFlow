// Package llm 实现 port.LLMClient：按配置分发到 OpenAI 兼容或 Anthropic Messages 适配器。
// 两个适配器的核心逻辑均移植自 pi（https://github.com/earendil-works/pi，
// MIT License, Copyright (c) 2025 Mario Zechner），见 openai.go / anthropic.go 头注。
package llm

import (
	"fmt"
	"net/http"
	"time"

	"reqflow/internal/port"
)

// Provider 支持的协议类型。
const (
	ProviderOpenAI    = "openai"    // OpenAI 兼容 /chat/completions（DeepSeek/GLM/Qwen/Kimi/SiliconFlow 等，默认）
	ProviderAnthropic = "anthropic" // Anthropic Messages 协议
)

// Options 客户端参数（由 cmd 从配置注入）。
type Options struct {
	Provider    string // openai（默认）| anthropic
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

// New 构造客户端；apiKey 为空时仍可构造，调用将返回明确错误（功能降级由 app 判断）。
func New(opt Options) port.LLMClient {
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Minute
	}
	if opt.Provider == "" {
		opt.Provider = ProviderOpenAI
	}
	http := &http.Client{Timeout: opt.Timeout}
	switch opt.Provider {
	case ProviderAnthropic:
		return &anthropicClient{opt: opt, http: http}
	default:
		return &openaiClient{opt: opt, http: http}
	}
}

func checkAvailable(opt Options) error {
	if opt.APIKey == "" || opt.BaseURL == "" || opt.Model == "" {
		return fmt.Errorf("LLM 未配置（base_url/api_key/model），请在 config.yaml 填写后重启")
	}
	return nil
}

func (c *openaiClient) available() error    { return checkAvailable(c.opt) }
func (c *anthropicClient) available() error { return checkAvailable(c.opt) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func timeNowMilli() int64 { return time.Now().UnixMilli() }

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
