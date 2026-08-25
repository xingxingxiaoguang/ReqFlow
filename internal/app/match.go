package app

import (
	"context"
	"sort"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ProjectMatch 项目推荐候选。
type ProjectMatch struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Score         float64 `json:"score"`
	MatchType     string  `json:"match_type"` // exact | semantic
	SuggestedName string  `json:"suggested_name"`
}

// DuplicateMatch 查重命中的相似工作项。
type DuplicateMatch struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Score     float64 `json:"score"`
	MatchType string  `json:"match_type"` // exact | semantic
}

// DuplicateResult 单条草稿的查重结论（Match 为 nil 表示新工作项）。
type DuplicateResult struct {
	Index int            `json:"index"`
	Match *DuplicateMatch `json:"match"`
}

// MatchService 两层匹配用例：归一化精确匹配前置（准标识符不被向量稀释），
// 未命中的再走 pgvector 语义匹配兜底「换了个说法」的近似。
type MatchService struct {
	projects   port.ProjectRepo
	workItems  port.WorkItemRepo
	embedder   port.Embedder
	topN       int
	threshold  float64
	queryBatch int // 语义查询批大小（一次往返摊薄 embedding 开销）
}

// NewMatchService 构造用例。topN 项目推荐数；threshold 查重相似度阈值。
func NewMatchService(projects port.ProjectRepo, workItems port.WorkItemRepo, embedder port.Embedder, topN int, threshold float64) *MatchService {
	if topN <= 0 {
		topN = 5
	}
	if threshold <= 0 || threshold >= 1 {
		threshold = 0.75
	}
	return &MatchService{projects: projects, workItems: workItems, embedder: embedder, topN: topN, threshold: threshold, queryBatch: 50}
}

// MatchProjects 为一组项目名推荐目标项目（精确 score=1 置顶，语义兜底）。
// embedding 未配置时仅做精确匹配。
func (s *MatchService) MatchProjects(ctx context.Context, names []string) ([]ProjectMatch, error) {
	projects, err := s.projects.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	exact := make(map[string]model.Project, len(projects))
	for _, p := range projects {
		key := logic.NormalizeForExactMatch(p.Name)
		if key != "" {
			if _, exists := exact[key]; !exists {
				exact[key] = p
			}
		}
	}

	var matches []ProjectMatch
	var semantic []string
	seen := map[string]bool{}
	for _, name := range names {
		if key := logic.NormalizeForExactMatch(name); key != "" {
			if hit, ok := exact[key]; ok {
				matches = append(matches, ProjectMatch{
					ID: hit.ID, Name: hit.Name, Score: 1, MatchType: "exact", SuggestedName: name,
				})
				seen[hit.ID] = true
				continue
			}
		}
		if !seen["name:"+name] {
			semantic = append(semantic, name)
			seen["name:"+name] = true
		}
	}

	if len(semantic) > 0 && s.embedder.Available() {
		for start := 0; start < len(semantic); start += s.queryBatch {
			end := min(start+s.queryBatch, len(semantic))
			batch := semantic[start:end]
			vecs, err := s.embedder.Generate(ctx, batch)
			if err != nil {
				return nil, err
			}
			for i, name := range batch {
				hits, err := s.projects.SearchSimilar(ctx, vecs[i], 3)
				if err != nil || len(hits) == 0 {
					continue
				}
				top := hits[0]
				matches = append(matches, ProjectMatch{
					ID: top.ID, Name: top.Name,
					Score:         logic.DistanceToScore(top.Distance),
					MatchType:     "semantic",
					SuggestedName: name,
				})
			}
		}
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if len(matches) > s.topN {
		matches = matches[:s.topN]
	}
	return matches, nil
}

// CheckDuplicates 对草稿做同项目查重：标题精确命中即重复（score=1），
// 其余语义检索 top1，达到阈值视为 Similar。
func (s *MatchService) CheckDuplicates(ctx context.Context, projectID string, items []DraftInput) ([]DuplicateResult, error) {
	// 第一层：项目内已同步工作项的标题精确匹配
	synced, _, err := s.workItems.ListActive(ctx, port.WorkItemFilter{ProjectID: projectID, Limit: 10000})
	if err != nil {
		return nil, err
	}
	exact := make(map[string]model.WorkItem, len(synced))
	for _, w := range synced {
		key := logic.NormalizeForExactMatch(w.Title)
		if key != "" {
			if _, exists := exact[key]; !exists {
				exact[key] = w
			}
		}
	}

	results := make([]DuplicateResult, len(items))
	var semanticIdx []int
	for i, it := range items {
		key := logic.NormalizeForExactMatch(it.Title)
		if key != "" {
			if hit, ok := exact[key]; ok {
				results[i] = DuplicateResult{Index: i, Match: &DuplicateMatch{
					ID: hit.ID, Title: hit.Title, Score: 1, MatchType: "exact",
				}}
				continue
			}
		}
		semanticIdx = append(semanticIdx, i)
	}

	// 第二层：批量向量查询，查询文本与写入侧格式对齐（标题为主、描述截 500 字补充上下文）
	if len(semanticIdx) > 0 && s.embedder.Available() {
		for start := 0; start < len(semanticIdx); start += s.queryBatch {
			end := min(start+s.queryBatch, len(semanticIdx))
			batchIdx := semanticIdx[start:end]
			docs := make([]string, len(batchIdx))
			for i, idx := range batchIdx {
				docs[i] = workItemDoc(items[idx].Title, truncateRunes(items[idx].Description, 500))
			}
			vecs, err := s.embedder.Generate(ctx, docs)
			if err != nil {
				return nil, err
			}
			for i, idx := range batchIdx {
				hits, err := s.workItems.SearchSimilar(ctx, vecs[i], projectID, 1)
				if err != nil || len(hits) == 0 {
					results[idx] = DuplicateResult{Index: idx, Match: nil}
					continue
				}
				score := logic.DistanceToScore(hits[0].Distance)
				if score >= s.threshold {
					results[idx] = DuplicateResult{Index: idx, Match: &DuplicateMatch{
						ID: hits[0].ID, Title: hits[0].Title, Score: score, MatchType: "semantic",
					}}
				} else {
					results[idx] = DuplicateResult{Index: idx, Match: nil}
				}
			}
		}
	} else {
		for _, idx := range semanticIdx {
			results[idx] = DuplicateResult{Index: idx, Match: nil}
		}
	}
	return results, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
