package app

import (
	"context"
	"encoding/json"
	"fmt"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// DraftInput 需求草稿的 HTTP 入参形状（app 层 DTO，port/domain 类型不外泄）。
type DraftInput struct {
	ProjectName        string  `json:"project_name"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	Priority           string  `json:"priority"`
	EstimatedHours     float64 `json:"estimated_hours"`
	StartAt            string  `json:"start_at"`
	EndAt              string  `json:"end_at"`
	TypeID             string  `json:"type_id"`
	AssigneeName       string  `json:"assignee_name"`
	State              string  `json:"state"`
	SolutionSuggestion string  `json:"solution_suggestion"`
}

func (d DraftInput) toModel() model.DraftItem {
	return model.DraftItem{
		ProjectName: d.ProjectName, Title: d.Title, Description: d.Description,
		Priority: d.Priority, EstimatedHours: d.EstimatedHours,
		StartAt: d.StartAt, EndAt: d.EndAt, TypeID: d.TypeID,
		AssigneeName: d.AssigneeName, State: d.State,
		SolutionSuggestion: d.SolutionSuggestion,
	}
}

// DraftSaveInput 门内草稿保存入参（草稿 + 可选明细 ID；ID 空则新建）。
type DraftSaveInput struct {
	ID    string     `json:"id"`
	Draft DraftInput `json:"draft"`
}

// toTaskItem 草稿保存入参 → 任务明细（status 重置为 pending）。
func (d DraftSaveInput) toTaskItem() model.TaskItem {
	return model.TaskItem{ID: d.ID, DraftItem: d.Draft.toModel(), Status: model.ItemStatusPending}
}

// DatasetWriteProgress 数据集写入进度。
type DatasetWriteProgress struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

// DatasetWriter 数据集生成用例：草稿 → 向量化（分批）→ 幂等写入数据集。
// 写操作在本地 DB（含向量），未发布数据集断点续跑 = 全量重建（ReplaceDatasetItems）。
type DatasetWriter struct {
	embedder  port.Embedder
	datasets  port.DatasetRepo
	batchSize int
}

// NewDatasetWriter 构造用例。
func NewDatasetWriter(embedder port.Embedder, datasets port.DatasetRepo, batchSize int) *DatasetWriter {
	if batchSize <= 0 {
		batchSize = 25
	}
	return &DatasetWriter{embedder: embedder, datasets: datasets, batchSize: batchSize}
}

// 向量文档格式（查询侧 app/match.go 语义层必须对齐：标题为主、描述截断）。
const datasetItemVectorDocFmt = "Title: %s\nDescription: %s"

// Write 把草稿写入数据集（含向量）。返回写入条数；embedding 未配置时降级纯精确匹配
// （向量留空，条目照常入库）。
func (s *DatasetWriter) Write(ctx context.Context, datasetID string, items []model.TaskItem,
	report func(DatasetWriteProgress)) (int, error) {
	if report == nil {
		report = func(DatasetWriteProgress) {}
	}
	if len(items) == 0 {
		return 0, nil
	}

	// 分批向量化（对齐 sync 时代的 batch 模式，缓解 embedding 服务压力）
	vectors := make([]port.DatasetItemVector, len(items))
	for i, it := range items {
		vectors[i] = port.DatasetItemVector{DatasetItem: model.DatasetItem{Fields: marshalJSON(it.DraftItem)}}
	}
	if s.embedder.Available() {
		done := 0
		for start := 0; start < len(vectors); start += s.batchSize {
			end := minInt(start+s.batchSize, len(vectors))
			texts := make([]string, 0, end-start)
			for _, v := range vectors[start:end] {
				f := requirementFieldsOf(v.Fields)
				texts = append(texts, fmt.Sprintf(datasetItemVectorDocFmt, f.Title, truncateRunes(f.Description, 500)))
			}
			embs, err := s.embedder.Generate(ctx, texts)
			if err != nil {
				return 0, fmt.Errorf("向量化失败: %w", err)
			}
			for j, emb := range embs {
				vectors[start+j].Embedding = emb
			}
			done = end
			report(DatasetWriteProgress{Current: done, Total: len(vectors)})
		}
	}

	if err := s.datasets.ReplaceDatasetItems(ctx, datasetID, vectors); err != nil {
		return 0, err
	}
	report(DatasetWriteProgress{Current: len(vectors), Total: len(vectors)})
	return len(vectors), nil
}

// requirementFieldsOf 解析需求条目字段（向量文档用）。
func requirementFieldsOf(fields string) model.DraftItem {
	var d model.DraftItem
	_ = json.Unmarshal([]byte(fields), &d)
	return d
}

func minInt(a, b int) int {
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
	return string(r[:n]) + "…"
}
