package embedding

import (
	"context"
	"fmt"
	"time"

	"reqflow/internal/port"
)

type DynamicEmbedder struct{ resolver port.PlatformConfigResolver }

func NewDynamic(resolver port.PlatformConfigResolver) port.Embedder {
	return &DynamicEmbedder{resolver: resolver}
}

func (c *DynamicEmbedder) Available() bool {
	if c == nil || c.resolver == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	config, err := c.resolver.ResolveEmbedding(ctx)
	return err == nil && New(Options{BaseURL: config.BaseURL, APIKey: config.APIKey, Model: config.Model}).Available()
}

func (c *DynamicEmbedder) Generate(ctx context.Context, texts []string) ([][]float32, error) {
	if c == nil || c.resolver == nil {
		return nil, fmt.Errorf("Embedding 平台配置解析器未初始化")
	}
	config, err := c.resolver.ResolveEmbedding(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取当前 Embedding 配置: %w", err)
	}
	return New(Options{BaseURL: config.BaseURL, APIKey: config.APIKey, Model: config.Model,
		BatchSize: config.BatchSize, Timeout: time.Duration(config.TimeoutMs) * time.Millisecond}).Generate(ctx, texts)
}

type DynamicReranker struct{ resolver port.PlatformConfigResolver }

func NewDynamicReranker(resolver port.PlatformConfigResolver) port.Reranker {
	return &DynamicReranker{resolver: resolver}
}

func (r *DynamicReranker) Available() bool {
	if r == nil || r.resolver == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	config, err := r.resolver.ResolveRerank(ctx)
	return err == nil && NewReranker(RerankerOptions{BaseURL: config.BaseURL,
		APIKey: config.APIKey, Model: config.Model}).Available()
}

func (r *DynamicReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]port.RerankResult, error) {
	if r == nil || r.resolver == nil {
		return nil, fmt.Errorf("Rerank 平台配置解析器未初始化")
	}
	config, err := r.resolver.ResolveRerank(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取当前 Rerank 配置: %w", err)
	}
	return NewReranker(RerankerOptions{BaseURL: config.BaseURL, APIKey: config.APIKey,
		Model: config.Model, Timeout: time.Duration(config.TimeoutMs) * time.Millisecond}).Rerank(ctx, query, documents, topN)
}
