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

	"reqflow/internal/port"
)

// RerankerOptions 复用 embedding 供应商的 BaseURL/APIKey；模型和超时可独立配置。
type RerankerOptions struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type Reranker struct {
	opt  RerankerOptions
	http *http.Client
}

func NewReranker(opt RerankerOptions) *Reranker {
	if strings.TrimSpace(opt.Model) == "" {
		opt.Model = "BAAI/bge-reranker-v2-m3"
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 60 * time.Second
	}
	return &Reranker{opt: opt, http: &http.Client{Timeout: opt.Timeout}}
}

func (r *Reranker) Available() bool {
	return r != nil && strings.TrimSpace(r.opt.BaseURL) != "" && strings.TrimSpace(r.opt.APIKey) != "" && strings.TrimSpace(r.opt.Model) != ""
}

func (r *Reranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]port.RerankResult, error) {
	if !r.Available() {
		return nil, fmt.Errorf("rerank 未配置，请在平台配置中激活可用配置")
	}
	if len(documents) == 0 {
		return []port.RerankResult{}, nil
	}
	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}
	body, err := json.Marshal(map[string]any{
		"model": r.opt.Model, "query": query, "documents": documents,
		"return_documents": false, "top_n": topN,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(r.opt.BaseURL, "/")+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.opt.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank 请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rerank HTTP %d: %s", resp.StatusCode, trunc(string(raw), 500))
	}
	var response struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("rerank 响应解析失败: %w", err)
	}
	results := make([]port.RerankResult, 0, len(response.Results))
	seen := make(map[int]bool, len(response.Results))
	for _, result := range response.Results {
		if result.Index < 0 || result.Index >= len(documents) || seen[result.Index] {
			return nil, fmt.Errorf("rerank 响应包含非法或重复 index %d", result.Index)
		}
		seen[result.Index] = true
		results = append(results, port.RerankResult{Index: result.Index, Score: result.RelevanceScore})
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("rerank 响应没有 results")
	}
	return results, nil
}
