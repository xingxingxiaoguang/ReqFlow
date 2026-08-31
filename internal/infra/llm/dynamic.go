package llm

import (
	"context"
	"fmt"
	"time"

	"reqflow/internal/port"
)

// DynamicClient 在每次模型调用前解析当前激活配置，因此切换配置无需重启服务。
type DynamicClient struct{ resolver port.PlatformConfigResolver }

func NewDynamic(resolver port.PlatformConfigResolver) port.LLMClient {
	return &DynamicClient{resolver: resolver}
}

func (c *DynamicClient) Stream(ctx context.Context, value *port.Context,
	onEvent func(port.AssistantEvent)) (*port.Message, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Stream(ctx, value, onEvent)
}

func (c *DynamicClient) Complete(ctx context.Context, value *port.Context) (*port.Message, error) {
	client, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	return client.Complete(ctx, value)
}

func (c *DynamicClient) Ping(ctx context.Context) error {
	client, err := c.client(ctx)
	if err != nil {
		return err
	}
	return client.Ping(ctx)
}

func (c *DynamicClient) client(ctx context.Context) (port.LLMClient, error) {
	if c == nil || c.resolver == nil {
		return nil, fmt.Errorf("LLM 平台配置解析器未初始化")
	}
	config, err := c.resolver.ResolveLLM(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取当前 LLM 配置: %w", err)
	}
	return New(Options{Provider: config.Provider, BaseURL: config.BaseURL, APIKey: config.APIKey,
		Model: config.Model, Temperature: config.Temperature, MaxTokens: config.MaxTokens,
		Timeout: time.Duration(config.TimeoutMs) * time.Millisecond}), nil
}
