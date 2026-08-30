package app

import (
	"context"
	"encoding/json"
	"fmt"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// DraftSaveInput 门内草稿保存入参（schema 字段袋 + 可选明细 ID；ID 空则新建）。
type DraftSaveInput struct {
	ID     string         `json:"id"`
	Fields map[string]any `json:"fields"`
}

// toTaskItem 草稿保存入参 → 任务明细（status 重置为 pending）。
func (d DraftSaveInput) toTaskItem() model.TaskItem {
	return model.TaskItem{ID: d.ID, Fields: marshalJSON(d.Fields), Status: model.ItemStatusPending}
}

/* ---- 写入目标（task → dataset 的标准化接缝） ---- */

// 数据集写入模式（对齐数据集成行业的 append/dedupe/overwrite 范式）。
// 目标数据集先于任务存在（创建任务时绑定，ready 空集或已有条目），三种策略均幂等
// ——断点续跑/失败重试/终态重写安全；不再有"写入中新建数据集"路径。
const (
	// WriteModeMerge 并入已有数据集（默认）：仅插入新条目，已存在的 key 跳过（insert-if-absent）。
	WriteModeMerge = "merge"
	// WriteModeUpsert 并入已有数据集：新条目插入，已存在 key 按内容更新（相同则跳过）。
	WriteModeUpsert = "upsert"
	// WriteModeReplace 覆盖本任务此前写入的条目后并入（同源重跑；不动其他来源的数据）。
	WriteModeReplace = "replace"
)

var writeModes = map[string]bool{
	WriteModeMerge: true, WriteModeUpsert: true, WriteModeReplace: true,
}

// DatasetTarget 生成数据集门的人工声明：以何种策略写入目标数据集。
// 目标数据集在创建任务时绑定（tasks.output_dataset_id）——字段定义随数据集带出；
// 本声明只决定写入策略，不再承担"新建数据集"职责（数据集先于任务存在）。
type DatasetTarget struct {
	Mode      string `json:"mode"`       // merge | upsert | replace
	DatasetID string `json:"dataset_id"` // 目标数据集（空 = 任务绑定的产出数据集）
}

// Normalize 校验并补默认值（mode 空 → merge）。
func (t DatasetTarget) Normalize() (DatasetTarget, error) {
	if t.Mode == "" {
		t.Mode = WriteModeMerge
	}
	if !writeModes[t.Mode] {
		return t, fmt.Errorf("不支持的写入模式: %s", t.Mode)
	}
	if t.DatasetID == "" {
		return t, fmt.Errorf("缺少目标数据集（请在创建任务时绑定）")
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

// 向量文档 body 截断长度常量已下沉 domain（logic.VectorBodyLimit）——写入/查询/指纹共用。

// Prepare 预览写入效果：schema 校验 + 目标数据集冲突分桶。不落库、不向量化。
// schema 由调用方从目标数据集行解析（字段定义归属数据集——写入谁就按谁的合同校验）。
func (s *DatasetWriter) Prepare(ctx context.Context, schema model.DatasetSchema, target DatasetTarget,
	taskID string, values []map[string]any) (*PreparedWrite, error) {
	target, err := target.Normalize()
	if err != nil {
		return nil, err
	}
	ds, err := s.datasets.GetDataset(ctx, target.DatasetID)
	if err != nil {
		return nil, fmt.Errorf("目标数据集不存在: %w", err)
	}
	prepared := &PreparedWrite{Target: target, Schema: schema,
		preview: WritePreview{Mode: target.Mode, DatasetID: target.DatasetID, DatasetName: ds.Name, Total: len(values)}}

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
		}
	}
	return prepared, nil
}

// Write 执行写入：仅对 insert/update 条目向量化（分批）→ 按模式幂等落库。
// replace 先清理本任务旧条目；merge/upsert 走 ON CONFLICT 幂等写入。
func (s *DatasetWriter) Write(ctx context.Context, datasetID, taskID string, prepared *PreparedWrite,
	report func(DatasetWriteProgress)) (WriteStats, error) {
	if report == nil {
		report = func(DatasetWriteProgress) {}
	}
	pending := filterByActions(prepared.Items, ActionInsert, ActionUpdate)
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
				texts = append(texts, logic.VectorDocOf(prepared.Schema, parseFieldsValues(v.Fields), logic.VectorBodyLimit))
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

	if prepared.Target.Mode == WriteModeReplace {
		if _, err := s.datasets.DeleteDatasetItemsBySource(ctx, datasetID, taskID); err != nil {
			return stats, err
		}
	}
	mode := port.UpsertInsertMissing
	if prepared.Target.Mode == WriteModeUpsert {
		mode = port.UpsertUpdateExisting
	}
	if err := s.datasets.UpsertDatasetItems(ctx, datasetID, taskID, vectors, mode); err != nil {
		return stats, err
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

/* ---- 字段袋解析 ---- */

// parseFieldsValues fields JSON → 字段值 map（查重语料/向量文档组装用；宽松解析）。
// TaskItem 的读侧统一入口是 model.TaskItem.Values()，此函数服务 DatasetItem.Fields。
func parseFieldsValues(fields string) map[string]any {
	var v map[string]any
	_ = json.Unmarshal([]byte(fields), &v)
	return v
}
