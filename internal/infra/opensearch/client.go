// Package opensearch 实现 Retrieval LexicalBackend：按 Dataset + Profile 建独立 BM25 索引。
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"reqflow/internal/port"
)

type Options struct {
	BaseURL     string
	Username    string
	Password    string
	IndexPrefix string
	Timeout     time.Duration
}

type Client struct {
	opt  Options
	http *http.Client
}

func New(opt Options) *Client {
	opt.BaseURL = strings.TrimSuffix(strings.TrimSpace(opt.BaseURL), "/")
	opt.IndexPrefix = strings.Trim(strings.ToLower(strings.TrimSpace(opt.IndexPrefix)), "-_")
	if opt.IndexPrefix == "" {
		opt.IndexPrefix = "reqflow"
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	return &Client{opt: opt, http: &http.Client{Timeout: opt.Timeout}}
}

func (c *Client) Available() bool { return c != nil && c.opt.BaseURL != "" }

func (c *Client) PhysicalIndex(logicalRef string) string {
	return c.opt.IndexPrefix + "-" + strings.Trim(strings.ToLower(logicalRef), "-_")
}

func (c *Client) Build(ctx context.Context, req port.LexicalBuildRequest) error {
	if !c.Available() {
		return fmt.Errorf("OpenSearch 未配置")
	}
	index := c.PhysicalIndex(req.IndexRef)
	if err := c.ensureIndex(ctx, index, req); err != nil {
		return err
	}
	if len(req.Documents) == 0 {
		return nil
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	for _, doc := range req.Documents {
		if err := encoder.Encode(map[string]any{"index": map[string]any{"_index": index, "_id": doc.DatasetItemID}}); err != nil {
			return err
		}
		if err := encoder.Encode(map[string]any{
			"dataset_item_id": doc.DatasetItemID,
			"source_seq":      doc.SourceSeq,
			"fields":          doc.Fields,
			"filters":         doc.Filters,
		}); err != nil {
			return err
		}
	}
	var response struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int `json:"status"`
			Error  any `json:"error"`
		} `json:"items"`
	}
	if err := c.request(ctx, http.MethodPost, "/_bulk?refresh=wait_for", &body, &response); err != nil {
		return fmt.Errorf("OpenSearch bulk 写入失败: %w", err)
	}
	if response.Errors {
		for _, item := range response.Items {
			for _, result := range item {
				if result.Status >= 400 {
					return fmt.Errorf("OpenSearch bulk item HTTP %d: %v", result.Status, result.Error)
				}
			}
		}
		return fmt.Errorf("OpenSearch bulk 返回未明错误")
	}
	return nil
}

func (c *Client) Count(ctx context.Context, indexRef string, sourceSeq int64) (int, error) {
	query := map[string]any{"query": map[string]any{"range": map[string]any{"source_seq": map[string]any{"lte": sourceSeq}}}}
	var response struct {
		Count int `json:"count"`
	}
	if err := c.jsonRequest(ctx, http.MethodPost, "/"+url.PathEscape(c.PhysicalIndex(indexRef))+"/_count", query, &response); err != nil {
		return 0, err
	}
	return response.Count, nil
}

func (c *Client) Search(ctx context.Context, req port.LexicalSearchRequest) ([]port.RankedHit, error) {
	fields := make([]string, 0, len(req.Fields))
	for name, weight := range req.Fields {
		fields = append(fields, fmt.Sprintf("fields.%s^%g", name, weight))
	}
	sort.Strings(fields)
	filters := []any{map[string]any{"range": map[string]any{"source_seq": map[string]any{"lte": req.SourceSeq}}}}
	filterNames := make([]string, 0, len(req.Filters))
	for name := range req.Filters {
		filterNames = append(filterNames, name)
	}
	sort.Strings(filterNames)
	for _, name := range filterNames {
		filters = append(filters, map[string]any{"terms": map[string]any{"filters." + name: req.Filters[name]}})
	}
	body := map[string]any{
		"size": req.Limit,
		"query": map[string]any{"bool": map[string]any{
			"must":   []any{map[string]any{"multi_match": map[string]any{"query": req.Query, "fields": fields, "type": "best_fields"}}},
			"filter": filters,
		}},
		"_source": []string{"dataset_item_id"},
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					DatasetItemID string `json:"dataset_item_id"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := c.jsonRequest(ctx, http.MethodPost, "/"+url.PathEscape(c.PhysicalIndex(req.IndexRef))+"/_search", body, &response); err != nil {
		return nil, err
	}
	out := make([]port.RankedHit, len(response.Hits.Hits))
	for i, hit := range response.Hits.Hits {
		out[i] = port.RankedHit{DatasetItemID: hit.Source.DatasetItemID, Rank: i + 1, Score: hit.Score}
	}
	return out, nil
}

func (c *Client) ensureIndex(ctx context.Context, index string, req port.LexicalBuildRequest) error {
	httpReq, err := c.newRequest(ctx, http.MethodHead, "/"+url.PathEscape(index), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("OpenSearch 检查索引失败: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("OpenSearch 检查索引 HTTP %d", resp.StatusCode)
	}
	fieldMappings := map[string]any{}
	for name := range req.Fields {
		fieldMappings[name] = map[string]any{"type": "text", "analyzer": req.Analyzer}
	}
	filterMappings := map[string]any{}
	for _, name := range req.Filters {
		filterMappings[name] = map[string]any{"type": "keyword", "ignore_above": 2048}
	}
	body := map[string]any{"mappings": map[string]any{
		"dynamic": "strict",
		"properties": map[string]any{
			"dataset_item_id": map[string]any{"type": "keyword"},
			"source_seq":      map[string]any{"type": "long"},
			"fields":          map[string]any{"type": "object", "dynamic": "strict", "properties": fieldMappings},
			"filters":         map[string]any{"type": "object", "dynamic": "strict", "properties": filterMappings},
		},
	}}
	if err := c.jsonRequest(ctx, http.MethodPut, "/"+url.PathEscape(index), body, nil); err != nil {
		// 并发创建时 resource_already_exists_exception 是幂等成功；重新 HEAD 确认。
		recheck, reqErr := c.newRequest(ctx, http.MethodHead, "/"+url.PathEscape(index), nil)
		if reqErr == nil {
			if checkResp, doErr := c.http.Do(recheck); doErr == nil {
				_ = checkResp.Body.Close()
				if checkResp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		return err
	}
	return nil
}

func (c *Client) jsonRequest(ctx context.Context, method, path string, payload any, dst any) error {
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			return err
		}
	}
	return c.request(ctx, method, path, &body, dst)
}

func (c *Client) request(ctx context.Context, method, path string, body io.Reader, dst any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 500))
	}
	if dst != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("响应 JSON 非法: %w", err)
		}
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if !c.Available() {
		return nil, fmt.Errorf("OpenSearch 未配置")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.opt.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.opt.Username != "" {
		req.SetBasicAuth(c.opt.Username, c.opt.Password)
	}
	return req, nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
