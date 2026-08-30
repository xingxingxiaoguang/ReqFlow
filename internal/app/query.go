package app

import (
	"context"
	"fmt"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// FieldCondition 字段过滤条件（HTTP 入参形状；仅 schema Filterable 字段可下推）。
type FieldCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"` // eq | ne | in | contains
	Value any    `json:"value"`
}

// DatasetQuery 统一查询：数据集范围 + 字段过滤 + 语义检索。
// 数据集浏览筛选、agent 工具、后续任务（如 Bug 分析）的输入选择共用本入口。
type DatasetQuery struct {
	DatasetID string           `json:"dataset_id"` // 空 = 按类型跨数据集
	Type      string           `json:"type"`
	Text      string           `json:"text"` // 语义查询（embedding 可用时；不可用降级纯过滤）
	Filters   []FieldCondition `json:"filters"`
	TopN      int              `json:"top_n"`
}

// QueryHit 查询命中（语义命中附带分数）。
type QueryHit struct {
	model.DatasetItem
	Score     float64 `json:"score,omitempty"`
	MatchType string  `json:"match_type,omitempty"` // semantic（过滤/精确命中无标记）
}

// DatasetQueryService 消费侧统一查询用例。
type DatasetQueryService struct {
	datasets port.DatasetRepo
	embedder port.Embedder
}

// NewDatasetQueryService 构造用例。
func NewDatasetQueryService(datasets port.DatasetRepo, embedder port.Embedder) *DatasetQueryService {
	return &DatasetQueryService{datasets: datasets, embedder: embedder}
}

// 语义检索扩量倍数：向量 topN 先扩量，字段过滤后截断（小数据量下的取舍）。
const semanticOverfetch = 3

// Query 执行查询：字段过滤下推 SQL；带文本时语义检索（在过滤范围内取 topN×3 再截断）。
// 字段条件按 schema 校验（Field 存在且 Filterable），防任意 JSON key 探测。
func (s *DatasetQueryService) Query(ctx context.Context, q DatasetQuery) ([]QueryHit, error) {
	if q.TopN <= 0 || q.TopN > 500 {
		q.TopN = 100
	}
	schema, err := s.schemaFor(ctx, q)
	if err != nil {
		return nil, err
	}
	conds := make([]port.FieldCondition, 0, len(q.Filters))
	for _, c := range q.Filters {
		f, ok := schema.Field(c.Field)
		if !ok || !f.Filterable {
			return nil, fmt.Errorf("字段 %s 不可作为筛选条件", c.Field)
		}
		if c.Op == "" {
			c.Op = "eq"
		}
		if c.Op == "in" {
			if vals, ok := c.Value.([]any); ok {
				strs := make([]string, 0, len(vals))
				for _, v := range vals {
					strs = append(strs, fmt.Sprintf("%v", v))
				}
				c.Value = strs
			}
		} else {
			c.Value = fmt.Sprintf("%v", c.Value)
		}
		conds = append(conds, port.FieldCondition{Field: c.Field, Op: c.Op, Value: c.Value})
	}
	filter := port.ItemFilter{DatasetID: q.DatasetID, Type: q.Type, Conds: conds}

	// 纯过滤（无语义文本）
	text := strings.TrimSpace(q.Text)
	if text == "" || !s.embedder.Available() {
		items, err := s.datasets.ListDatasetItemsFiltered(ctx, filter)
		if err != nil {
			return nil, err
		}
		hits := make([]QueryHit, 0, len(items))
		for _, it := range items {
			hits = append(hits, QueryHit{DatasetItem: it})
		}
		return limitHits(hits, q.TopN), nil
	}

	// 语义：查询向量（与写入侧同一 schema 组装规则，保证向量空间对齐）
	queryDoc := logic.VectorDocOf(schema, map[string]any{
		schemaTitleKey(schema): text,
	}, logic.VectorBodyLimit)
	emb, err := s.embedder.Generate(ctx, []string{queryDoc})
	if err != nil {
		return nil, fmt.Errorf("查询向量化失败: %w", err)
	}
	sims, err := s.datasets.SearchSimilarDatasetItemsFiltered(ctx, emb[0], filter, q.TopN*semanticOverfetch)
	if err != nil {
		return nil, err
	}
	hits := make([]QueryHit, 0, len(sims))
	for _, sim := range sims {
		hits = append(hits, QueryHit{
			DatasetItem: sim.DatasetItem,
			Score:       logic.DistanceToScore(sim.Distance),
			MatchType:   "semantic",
		})
	}
	return limitHits(hits, q.TopN), nil
}

// SearchFTS 数据集内全文检索（PG 表达式 tsvector）：FTS 字段取自数据集自身 schema
// （字段定义归属数据集），多字段 OR 命中动态表达式索引，按相关度排序。
func (s *DatasetQueryService) SearchFTS(ctx context.Context, datasetID, q string, n int) ([]model.DatasetItem, error) {
	ds, err := s.datasets.GetDataset(ctx, datasetID)
	if err != nil {
		return nil, fmt.Errorf("数据集不存在: %w", err)
	}
	schema, ok := model.ParseDatasetSchema(ds.Schema)
	if !ok {
		return nil, fmt.Errorf("数据集「%s」缺少字段定义", ds.Name)
	}
	fields := make([]string, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if f.FTS {
			fields = append(fields, f.Key)
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("数据集「%s」没有可全文检索的字段（schema 未标记 fts）", ds.Name)
	}
	return s.datasets.SearchDatasetItemsFTS(ctx, datasetID, fields, q, n)
}

// schemaFor 查询口径的字段定义：指定数据集时按其自身 schema（字段定义归属数据集，
// 同类型的不同数据集可各自演进）；跨类型查询按类型模板解析。
func (s *DatasetQueryService) schemaFor(ctx context.Context, q DatasetQuery) (model.DatasetSchema, error) {
	if q.DatasetID != "" {
		ds, err := s.datasets.GetDataset(ctx, q.DatasetID)
		if err != nil {
			return model.DatasetSchema{}, fmt.Errorf("数据集不存在: %w", err)
		}
		schema, ok := model.ParseDatasetSchema(ds.Schema)
		if !ok {
			return model.DatasetSchema{}, fmt.Errorf("数据集「%s」缺少字段定义", ds.Name)
		}
		return schema, nil
	}
	schema, ok := effectiveSchemaOf(q.Type)
	if !ok {
		return model.DatasetSchema{}, fmt.Errorf("未注册的数据集类型: %s", q.Type)
	}
	return schema, nil
}

// schemaTitleKey 取 schema 的向量标题字段 key（语义查询文本挂载位）。
func schemaTitleKey(schema model.DatasetSchema) string {
	for _, f := range schema.Fields {
		if f.InVector == model.VectorTitle {
			return f.Key
		}
	}
	return schema.Fields[0].Key
}

func limitHits(hits []QueryHit, n int) []QueryHit {
	if len(hits) > n {
		return hits[:n]
	}
	return hits
}
