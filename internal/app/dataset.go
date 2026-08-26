package app

import (
	"context"
	"encoding/json"
	"fmt"

	"reqflow/internal/domain/logic"
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

/* ---- 写入目标（task → dataset 的标准化接缝） ---- */

// 数据集写入模式（对齐数据集成行业的 append/dedupe/overwrite 范式）。
const (
	// WriteModeCreate 新建数据集（默认；断点续跑为同一数据集全量重建）。
	WriteModeCreate = "create"
	// WriteModeMerge 并入已有数据集：仅插入新条目，已存在的 key 跳过（insert-if-absent）。
	WriteModeMerge = "merge"
	// WriteModeUpsert 并入已有数据集：新条目插入，已存在 key 按内容更新（相同则跳过）。
	WriteModeUpsert = "upsert"
	// WriteModeReplace 覆盖本任务此前写入的条目后并入（同源重跑；不动其他来源的数据）。
	WriteModeReplace = "replace"
)

var writeModes = map[string]bool{
	WriteModeCreate: true, WriteModeMerge: true, WriteModeUpsert: true, WriteModeReplace: true,
}

// DatasetTarget 生成数据集门的人工声明：写入哪个数据集、以何种策略写入。
type DatasetTarget struct {
	Mode      string `json:"mode"`                   // create | merge | upsert | replace
	DatasetID string `json:"dataset_id,omitempty"`   // merge/upsert/replace：目标数据集
	Name      string `json:"dataset_name,omitempty"` // create：新建数据集命名
}

// Normalize 校验并补默认值（mode 空 → create）。
func (t DatasetTarget) Normalize() (DatasetTarget, error) {
	if t.Mode == "" {
		t.Mode = WriteModeCreate
	}
	if !writeModes[t.Mode] {
		return t, fmt.Errorf("不支持的写入模式: %s", t.Mode)
	}
	if t.Mode == WriteModeCreate && t.Name == "" {
		return t, fmt.Errorf("新建数据集需要命名")
	}
	if t.Mode != WriteModeCreate && t.DatasetID == "" {
		return t, fmt.Errorf("该写入模式需要选择目标数据集")
	}
	return t, nil
}

// DatasetWriteProgress 数据集写入进度。
type DatasetWriteProgress struct {
	Current int `json:"current"`
	Total   int `json:"total"`
}

// 写入动作（预览分桶）。
const (
	ActionInsert    = "insert"    // 新增
	ActionUpdate    = "update"    // 更新已存在条目
	ActionUnchanged = "unchanged" // 内容未变，跳过（免重嵌）
	ActionInvalid   = "invalid"   // schema 校验不过，跳过
)

// PreparedItem 预分桶后的单条写入项。
type PreparedItem struct {
	Values      map[string]any
	Fields      string // values 序列化 JSON（入库形状）
	Key         string
	Fingerprint string
	Action      string
	InvalidMsg  string
}

// WritePreview 落库前的冲突预览（生成数据集门展示）。
type WritePreview struct {
	Mode        string   `json:"mode"`
	DatasetID   string   `json:"dataset_id,omitempty"`
	DatasetName string   `json:"dataset_name,omitempty"`
	Insert      int      `json:"insert"`
	Update      int      `json:"update"`
	Unchanged   int      `json:"unchanged"`
	Invalid     int      `json:"invalid"`
	Total       int      `json:"total"`
	Errors      []string `json:"errors,omitempty"`
}

// WriteStats 实际写入统计。
type WriteStats struct {
	Written int // insert + update
	Skipped int // unchanged + invalid
}

// PreparedWrite 两阶段写入的中间产物：Prepare（预览）与 Write（执行）共享同一分桶。
type PreparedWrite struct {
	Target  DatasetTarget
	Schema  model.DatasetSchema
	Fresh   bool // create 模式：目标数据集全新，全部 insert（全量重建）
	Items   []PreparedItem
	preview WritePreview
}

func (p *PreparedWrite) Preview() WritePreview { return p.preview }

// DatasetWriter 数据集写入用例：schema 校验 → 分桶（新增/更新/无变化/非法）
// → 仅对有变化的条目向量化 → 按模式幂等写入。
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

// vectorBodyLimit 向量文档中 body 字段的总截断长度（与查询侧组装对齐）。
const vectorBodyLimit = 500

// Prepare 预览写入效果：schema 校验 + 目标数据集冲突分桶。不落库、不向量化。
func (s *DatasetWriter) Prepare(ctx context.Context, schema model.DatasetSchema, target DatasetTarget,
	taskID string, values []map[string]any) (*PreparedWrite, error) {
	target, err := target.Normalize()
	if err != nil {
		return nil, err
	}
	prepared := &PreparedWrite{Target: target, Schema: schema,
		preview: WritePreview{Mode: target.Mode, DatasetID: target.DatasetID, DatasetName: target.Name, Total: len(values)}}

	// 条目身份（key/指纹）与 schema 校验
	for _, v := range values {
		item := PreparedItem{Values: v, Fields: marshalJSON(v)}
		item.Key = logic.ItemKeyOf(schema, v)
		item.Fingerprint = logic.FingerprintOf(schema, v)
		if err := logic.ValidateValues(schema, v); err != nil {
			item.Action, item.InvalidMsg = ActionInvalid, err.Error()
		}
		prepared.Items = append(prepared.Items, item)
	}

	if target.Mode == WriteModeCreate {
		// 新数据集（或断点续跑全量重建）：无冲突概念，合法条目全部 insert
		prepared.Fresh = true
	} else {
		ds, err := s.datasets.GetDataset(ctx, target.DatasetID)
		if err != nil {
			return nil, fmt.Errorf("目标数据集不存在: %w", err)
		}
		if ds.Type != schema.Type {
			return nil, fmt.Errorf("目标数据集类型 %s 与任务产出类型 %s 不匹配", ds.Type, schema.Type)
		}
		prepared.preview.DatasetName = ds.Name
		keyMap, err := s.datasets.GetDatasetItemKeyMap(ctx, target.DatasetID)
		if err != nil {
			return nil, err
		}
		for i := range prepared.Items {
			it := &prepared.Items[i]
			if it.Action == ActionInvalid {
				continue
			}
			existing, ok := keyMap[it.Key]
			// replace 模式：本任务旧条目将被清理，视同不存在
			if ok && target.Mode == WriteModeReplace && existing.SourceTaskID == taskID {
				ok = false
			}
			switch {
			case !ok:
				it.Action = ActionInsert
			case target.Mode == WriteModeMerge || existing.Fingerprint == it.Fingerprint:
				it.Action = ActionUnchanged
			default:
				it.Action = ActionUpdate
			}
		}
	}

	for i := range prepared.Items {
		it := &prepared.Items[i]
		switch it.Action {
		case ActionInsert:
			prepared.preview.Insert++
		case ActionUpdate:
			prepared.preview.Update++
		case ActionUnchanged:
			prepared.preview.Unchanged++
		case ActionInvalid:
			prepared.preview.Invalid++
			if len(prepared.preview.Errors) < 5 {
				prepared.preview.Errors = append(prepared.preview.Errors, it.InvalidMsg)
			}
		default: // create 模式合法条目尚未定动作
			it.Action = ActionInsert
			prepared.preview.Insert++
		}
	}
	return prepared, nil
}

// Write 执行写入：仅对 insert/update 条目向量化（分批）→ 按模式落库。
// create 走全量重建（断点续跑幂等）；replace 先清理本任务旧条目。
func (s *DatasetWriter) Write(ctx context.Context, datasetID, taskID string, prepared *PreparedWrite,
	report func(DatasetWriteProgress)) (WriteStats, error) {
	if report == nil {
		report = func(DatasetWriteProgress) {}
	}
	var pending []PreparedItem
	if prepared.Fresh {
		pending = filterByActions(prepared.Items, ActionInsert)
	} else {
		pending = filterByActions(prepared.Items, ActionInsert, ActionUpdate)
	}
	stats := WriteStats{Skipped: len(prepared.Items) - len(pending)}

	vectors := make([]port.DatasetItemVector, len(pending))
	for i, it := range pending {
		vectors[i] = port.DatasetItemVector{
			DatasetItem: model.DatasetItem{
				Fields: it.Fields, ItemKey: it.Key,
				Fingerprint: it.Fingerprint, SourceTaskID: taskID,
			},
		}
	}

	// 分批向量化（unchanged 条目不占用 embedding 配额）
	if s.embedder.Available() && len(vectors) > 0 {
		done := 0
		for start := 0; start < len(vectors); start += s.batchSize {
			end := min(start+s.batchSize, len(vectors))
			texts := make([]string, 0, end-start)
			for _, v := range vectors[start:end] {
				texts = append(texts, logic.VectorDocOf(prepared.Schema, parseFieldsValues(v.Fields), vectorBodyLimit))
			}
			embs, err := s.embedder.Generate(ctx, texts)
			if err != nil {
				return stats, fmt.Errorf("向量化失败: %w", err)
			}
			for j, emb := range embs {
				vectors[start+j].Embedding = emb
			}
			done = end
			report(DatasetWriteProgress{Current: done, Total: len(vectors)})
		}
	}

	switch {
	case prepared.Fresh:
		// embedding 未配置时降级纯精确匹配（向量留空，条目照常入库）
		if err := s.datasets.ReplaceDatasetItems(ctx, datasetID, vectors); err != nil {
			return stats, err
		}
	case prepared.Target.Mode == WriteModeReplace:
		if _, err := s.datasets.DeleteDatasetItemsBySource(ctx, datasetID, taskID); err != nil {
			return stats, err
		}
		fallthrough
	default:
		mode := port.UpsertInsertMissing
		if prepared.Target.Mode == WriteModeUpsert {
			mode = port.UpsertUpdateExisting
		}
		if err := s.datasets.UpsertDatasetItems(ctx, datasetID, taskID, vectors, mode); err != nil {
			return stats, err
		}
	}
	stats.Written = len(vectors)
	report(DatasetWriteProgress{Current: len(vectors), Total: len(vectors)})
	return stats, nil
}

func filterByActions(items []PreparedItem, actions ...string) []PreparedItem {
	want := make(map[string]bool, len(actions))
	for _, a := range actions {
		want[a] = true
	}
	var out []PreparedItem
	for _, it := range items {
		if want[it.Action] {
			out = append(out, it)
		}
	}
	return out
}

/* ---- 需求条目字段（DraftItem → schema values） ---- */

// draftValuesOf 需求草稿 → schema 字段值（requirement 专属转换；新任务类型提供各自的转换器）。
func draftValuesOf(d model.DraftItem) map[string]any {
	return map[string]any{
		"title":               d.Title,
		"project_name":        d.ProjectName,
		"description":         d.Description,
		"solution_suggestion": d.SolutionSuggestion,
		"priority":            d.Priority,
		"type_id":             d.TypeID,
		"estimated_hours":     d.EstimatedHours,
		"start_at":            d.StartAt,
		"end_at":              d.EndAt,
		"assignee_name":       d.AssigneeName,
		"state":               d.State,
	}
}

// parseFieldsValues fields JSON → 字段值 map（向量文档组装用；宽松解析）。
func parseFieldsValues(fields string) map[string]any {
	var v map[string]any
	_ = json.Unmarshal([]byte(fields), &v)
	return v
}

// requirementFieldsOf 解析需求条目字段（查重展示等仍需强类型处）。
func requirementFieldsOf(fields string) model.DraftItem {
	var d model.DraftItem
	_ = json.Unmarshal([]byte(fields), &d)
	return d
}
