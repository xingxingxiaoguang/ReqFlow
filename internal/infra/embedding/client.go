// Package embedding 实现 port.Embedder：OpenAI 兼容 /embeddings（如硅基流动 BAAI/bge-m3）。
package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Options 客户端参数（由 cmd 从配置注入）。
type Options struct {
	BaseURL   string
	APIKey    string
	Model     string
	BatchSize int
	Timeout   time.Duration
}

// Client 向量化客户端。
type Client struct {
	opt  Options
	http *http.Client
}

// New 构造客户端；未配置密钥时 Available()=false，语义匹配自动降级。
func New(opt Options) *Client {
	if opt.BatchSize <= 0 {
		opt.BatchSize = 32
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	return &Client{opt: opt, http: &http.Client{Timeout: opt.Timeout}}
}

// Available 是否已配置可用。
func (c *Client) Available() bool {
	return c.opt.APIKey != "" && c.opt.BaseURL != "" && c.opt.Model != ""
}

// Generate 批量向量化（内部按 batch_size 分批调用，容忍响应乱序按 index 归位）。
func (c *Client) Generate(ctx context.Context, texts []string) ([][]float32, error) {
	if !c.Available() {
		return nil, fmt.Errorf("embedding 未配置（base_url/api_key/model），请在平台配置中激活可用配置")
	}
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += c.opt.BatchSize {
		end := start + c.opt.BatchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := c.batch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		copy(out[start:end], vecs)
	}
	return out, nil
}

func (c *Client) batch(ctx context.Context, texts []string) ([][]float32, error) {
	buf, err := json.Marshal(map[string]any{"model": c.opt.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.opt.BaseURL, "/")+"/embeddings", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.opt.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("embedding HTTP %d: %s", resp.StatusCode, trunc(string(body), 300))
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("embedding 响应解析失败: %w", err)
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	for i, v := range vecs {
		if v == nil {
			return nil, fmt.Errorf("embedding 响应缺少第 %d 条输入的向量", i)
		}
	}
	return vecs, nil
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
