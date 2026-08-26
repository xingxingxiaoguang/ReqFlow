package app

import (
	"context"
	"fmt"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// DuplicateMatch 查重命中的已有需求条目。
type DuplicateMatch struct {
	ID        string  `json:"id"`
	DatasetID string  `json:"dataset_id"`
	Title     string  `json:"title"`
	Score     float64 `json:"score"`
	MatchType string  `json:"match_type"` // exact | semantic
}

// DuplicateResult 单条草稿的查重结果。
type DuplicateResult struct {
	Index int             `json:"index"`
	Match *DuplicateMatch `json:"match"` // null = 无命中
}

// MatchService 查重用例：需求草稿 vs 已有需求数据集（跨数据集，两层匹配）。
type MatchService struct {
	datasets  port.DatasetRepo
	embedder  port.Embedder
	threshold float64
}

// NewMatchService 构造用例。
func NewMatchService(datasets port.DatasetRepo, embedder port.Embedder, threshold float64) *MatchService {
	return &MatchService{datasets: datasets, embedder: embedder, threshold: threshold}
}

// CheckDuplicates 批量查重：
// 1. 精确层：归一化精确匹配（保护版本号/工单号等关键 token），命中 score=1；
// 2. 语义层：仅对未命中项批量 embedding → SearchSimilarDatasetItems（cosine 距离换算分数），
//    达到阈值判为疑似重复。embedding 未配置时精确层照跑（降级）。
func (s *MatchService) CheckDuplicates(ctx context.Context, items []DraftInput) ([]DuplicateResult, error) {
	corpus, err := s.datasets.ListDatasetItemsByType(ctx, model.DatasetTypeRequirement)
	if err != nil {
		return nil, err
	}

	// 精确层索引：归一化标题 → 命中条目
	exactIdx := make(map[string]model.DatasetItem, len(corpus))
	for _, it := range corpus {
		key := logic.NormalizeForExactMatch(requirementTitle(it.Fields))
		if key != "" {
			if _, dup := exactIdx[key]; !dup {
				exactIdx[key] = it
			}
		}
	}

	results := make([]DuplicateResult, len(items))
	var pending []int // 需走语义层的下标
	for i, d := range items {
		key := logic.NormalizeForExactMatch(d.Title)
		if it, ok := exactIdx[key]; ok && key != "" {
			results[i] = DuplicateResult{Index: i, Match: s.matchOf(it, 1, "exact")}
			continue
		}
		results[i] = DuplicateResult{Index: i}
		pending = append(pending, i)
	}

	if s.embedder.Available() && len(pending) > 0 {
		texts := make([]string, len(pending))
		for k, i := range pending {
			texts[k] = fmt.Sprintf(datasetItemVectorDocFmt, items[i].Title, truncateRunes(items[i].Description, 500))
		}
		embs, err := s.embedder.Generate(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("向量化失败: %w", err)
		}
		for k, emb := range embs {
			sims, err := s.datasets.SearchSimilarDatasetItems(ctx, emb, model.DatasetTypeRequirement, 1)
			if err != nil {
				return nil, err
			}
			if len(sims) > 0 {
				score := logic.DistanceToScore(sims[0].Distance)
				if score >= s.threshold {
					results[pending[k]].Match = s.matchOf(sims[0].DatasetItem, score, "semantic")
				}
			}
		}
	}
	return results, nil
}

func (s *MatchService) matchOf(it model.DatasetItem, score float64, matchType string) *DuplicateMatch {
	return &DuplicateMatch{
		ID: it.ID, DatasetID: it.DatasetID,
		Title: requirementTitle(it.Fields), Score: score, MatchType: matchType,
	}
}

// requirementTitle 解析需求条目字段取标题（查重展示/精确索引用）。
func requirementTitle(fields string) string {
	return requirementFieldsOf(fields).Title
}
