package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type DatasetRepo struct {
	db       *gorm.DB
	tsConfig string // PG 全文检索分词配置（表达式索引与查询两侧必须一致）
}

func NewDatasetRepo(db *gorm.DB, tsConfig string) *DatasetRepo {
	if strings.TrimSpace(tsConfig) == "" {
		tsConfig = "simple"
	}
	return &DatasetRepo{db: db, tsConfig: tsConfig}
}

func (r *DatasetRepo) CreateDataset(ctx context.Context, d *model.Dataset) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now()
	d.CreatedAt, d.UpdatedAt = now, now
	return r.db.WithContext(ctx).Create(datasetToRow(d)).Error
}

func (r *DatasetRepo) UpdateDataset(ctx context.Context, d *model.Dataset) error {
	d.UpdatedAt = time.Now()
	return r.db.WithContext(ctx).Model(&datasetRow{}).
		Where("id = ?", d.ID).
		Updates(map[string]any{
			"status":      d.Status,
			"item_count":  d.ItemCount,
			"description": d.Description,
			"tags":        encodeTags(d.Tags),
			"updated_at":  d.UpdatedAt,
		}).Error
}

// UpdateDatasetSchema 数据集字段定义受控更新（只动 schema/schema_version/updated_at，
// 不触碰写入器持有的 status/item_count——避免与写入路径竞态覆盖）。
func (r *DatasetRepo) UpdateDatasetSchema(ctx context.Context, datasetID, payload string, version int) error {
	return r.db.WithContext(ctx).Model(&datasetRow{}).
		Where("id = ?", datasetID).
		Updates(map[string]any{
			"schema":         payload,
			"schema_version": version,
			"updated_at":     time.Now(),
		}).Error
}

func (r *DatasetRepo) ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := r.db.WithContext(ctx)
	if typ != "" {
		q = q.Where("type = ?", typ)
	}
	var rows []datasetRow
	if err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.Dataset, len(rows))
	for i, row := range rows {
		out[i] = datasetToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) GetDataset(ctx context.Context, id string) (*model.Dataset, error) {
	var row datasetRow
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; err != nil {
		return nil, err
	}
	d := datasetToModel(&row)
	return &d, nil
}

func (r *DatasetRepo) CountDatasets(ctx context.Context, typ string) (int64, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&datasetRow{})
	if typ != "" {
		q = q.Where("type = ?", typ)
	}
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DatasetRepo) CountDatasetItems(ctx context.Context, typ string) (int64, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&datasetItemRow{}).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id")
	if typ != "" {
		q = q.Where("datasets.type = ?", typ)
	}
	if err := q.Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DatasetRepo) ReplaceDatasetItems(ctx context.Context, datasetID string, items []port.DatasetItemVector) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("dataset_id = ?", datasetID).Delete(&datasetItemRow{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		rows := make([]datasetItemRow, len(items))
		now := time.Now()
		for i, it := range items {
			id := it.ID
			if id == "" {
				id = uuid.NewString()
			}
			rows[i] = datasetItemRow{
				ID: id, DatasetID: datasetID, Fields: it.Fields,
				ItemKey: it.ItemKey, Fingerprint: it.Fingerprint,
				SourceTaskID: strPtr(it.SourceTaskID),
				Embedding:    vectorPtr(it.Embedding), CreatedAt: now, UpdatedAt: now,
			}
		}
		return tx.Create(&rows).Error
	})
}

// upsertBatchSize 单批 VALUES 行数（参数上限 65535 的安全余量：每行 6 参）。
const upsertBatchSize = 2000

func (r *DatasetRepo) UpsertDatasetItems(ctx context.Context, datasetID, sourceTaskID string,
	items []port.DatasetItemVector, mode port.UpsertMode) error {
	if len(items) == 0 {
		return nil
	}
	var srcPtr *string
	if sourceTaskID != "" {
		srcPtr = &sourceTaskID
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for start := 0; start < len(items); start += upsertBatchSize {
			end := min(start+upsertBatchSize, len(items))
			var sb strings.Builder
			sb.WriteString(`INSERT INTO dataset_items
				(dataset_id, fields, item_key, fingerprint, source_task_id, embedding, updated_at)
				VALUES `)
			args := make([]any, 0, (end-start)*6+2)
			for i, it := range items[start:end] {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString("(?, ?::jsonb, ?, ?, ?, ?, now())")
				args = append(args, datasetID, it.Fields, it.ItemKey, it.Fingerprint, srcPtr, vectorPtr(it.Embedding))
			}
			sb.WriteString(` ON CONFLICT (dataset_id, item_key) WHERE item_key <> '' `)
			if mode == port.UpsertUpdateExisting {
				// 指纹相同不动行（跳过重嵌与 updated_at 抖动）；embedding 为空保留旧向量
				sb.WriteString(`DO UPDATE SET fields = EXCLUDED.fields,
					fingerprint = EXCLUDED.fingerprint,
					source_task_id = EXCLUDED.source_task_id,
					embedding = COALESCE(EXCLUDED.embedding, dataset_items.embedding),
					updated_at = now()
					WHERE dataset_items.fingerprint <> EXCLUDED.fingerprint`)
			} else {
				sb.WriteString(`DO NOTHING`)
			}
			if err := tx.Exec(sb.String(), args...).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *DatasetRepo) DeleteDatasetItemsBySource(ctx context.Context, datasetID, sourceTaskID string) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("dataset_id = ? AND source_task_id = ?", datasetID, sourceTaskID).
		Delete(&datasetItemRow{})
	return res.RowsAffected, res.Error
}

func (r *DatasetRepo) GetDatasetItemKeyMap(ctx context.Context, datasetID string) (map[string]port.DatasetItemKeyInfo, error) {
	var rows []struct {
		ItemKey      string  `gorm:"column:item_key"`
		ID           string  `gorm:"column:id"`
		Fingerprint  string  `gorm:"column:fingerprint"`
		SourceTaskID *string `gorm:"column:source_task_id"`
	}
	if err := r.db.WithContext(ctx).
		Model(&datasetItemRow{}).
		Select("item_key, id, fingerprint, source_task_id").
		Where("dataset_id = ? AND item_key <> ''", datasetID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]port.DatasetItemKeyInfo, len(rows))
	for _, row := range rows {
		out[row.ItemKey] = port.DatasetItemKeyInfo{ID: row.ID, Fingerprint: row.Fingerprint, SourceTaskID: strVal(row.SourceTaskID)}
	}
	return out, nil
}

func (r *DatasetRepo) CountDatasetItemsOfDataset(ctx context.Context, datasetID string) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&datasetItemRow{}).
		Where("dataset_id = ?", datasetID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func (r *DatasetRepo) ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error) {
	var rows []datasetItemRow
	if err := r.db.WithContext(ctx).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id").
		Where("datasets.type = ?", typ).
		Order("dataset_items.created_at ASC, dataset_items.id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = datasetItemRowToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) ListDatasetItems(ctx context.Context, datasetID string, limit int) ([]model.DatasetItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var rows []datasetItemRow
	if err := r.db.WithContext(ctx).Where("dataset_id = ?", datasetID).
		Order("created_at ASC, id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = datasetItemRowToModel(&row)
	}
	return out, nil
}

// fieldCondSQL 单个字段过滤条件 → SQL 片段与参数（fields 为原生 JSONB，直接取路径）。
func fieldCondSQL(cond port.FieldCondition) (string, any, error) {
	extract := "fields ->> '" + strings.ReplaceAll(cond.Field, "'", "''") + "'"
	switch cond.Op {
	case "eq":
		return extract + " = ?", cond.Value, nil
	case "ne":
		return extract + " <> ?", cond.Value, nil
	case "contains":
		return extract + " ILIKE ?", "%" + fmt.Sprintf("%v", cond.Value) + "%", nil
	case "in":
		vals, _ := cond.Value.([]string)
		if len(vals) == 0 {
			return "", nil, fmt.Errorf("in 条件 %s 值为空", cond.Field)
		}
		return extract + " = ANY(?)", vals, nil
	default:
		return "", nil, fmt.Errorf("不支持的字段过滤操作: %s", cond.Op)
	}
}

// applyItemFilter 范围（数据集/类型）+ 字段过滤 → 查询子句。
func applyItemFilter(q *gorm.DB, f port.ItemFilter) (*gorm.DB, error) {
	if f.DatasetID != "" {
		q = q.Where("dataset_items.dataset_id = ?", f.DatasetID)
	} else if f.Type != "" {
		q = q.Where("datasets.type = ?", f.Type)
	}
	for _, cond := range f.Conds {
		frag, arg, err := fieldCondSQL(cond)
		if err != nil {
			return nil, err
		}
		q = q.Where(frag, arg)
	}
	return q, nil
}

func (r *DatasetRepo) ListDatasetItemsFiltered(ctx context.Context, f port.ItemFilter) ([]model.DatasetItem, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := r.db.WithContext(ctx).Model(&datasetItemRow{}).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id")
	q, err := applyItemFilter(q, f)
	if err != nil {
		return nil, err
	}
	var rows []datasetItemRow
	if err := q.Order("dataset_items.created_at ASC, dataset_items.id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = datasetItemRowToModel(&row)
	}
	return out, nil
}

func (r *DatasetRepo) SearchSimilarDatasetItemsFiltered(ctx context.Context, vec []float32,
	f port.ItemFilter, n int) ([]port.SimilarDatasetItem, error) {
	var rows []struct {
		ID           string  `gorm:"column:id"`
		DatasetID    string  `gorm:"column:dataset_id"`
		Fields       string  `gorm:"column:fields"`
		ItemKey      string  `gorm:"column:item_key"`
		Fingerprint  string  `gorm:"column:fingerprint"`
		SourceTaskID *string `gorm:"column:source_task_id"`
		Distance     float64 `gorm:"column:distance"`
	}
	q := r.db.WithContext(ctx).
		Model(&datasetItemRow{}).
		Joins("JOIN datasets ON datasets.id = dataset_items.dataset_id").
		Select("dataset_items.id, dataset_items.dataset_id, dataset_items.fields, dataset_items.item_key, dataset_items.fingerprint, dataset_items.source_task_id, dataset_items.embedding <=> ? AS distance", pgvector.NewVector(vec)).
		Where("dataset_items.embedding IS NOT NULL")
	q, err := applyItemFilter(q, f)
	if err != nil {
		return nil, err
	}
	if err := q.Order("distance").Limit(n).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]port.SimilarDatasetItem, len(rows))
	for i, row := range rows {
		out[i] = port.SimilarDatasetItem{
			DatasetItem: model.DatasetItem{
				ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields,
				ItemKey: row.ItemKey, Fingerprint: row.Fingerprint,
				SourceTaskID: strVal(row.SourceTaskID),
			},
			Distance: row.Distance,
		}
	}
	return out, nil
}

func (r *DatasetRepo) SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]port.SimilarDatasetItem, error) {
	// 显式列映射（勿用嵌套结构体 Scan——GORM 对带 TableName 的嵌入结构映射不全，fields 会丢）
	var rows []struct {
		ID        string  `gorm:"column:id"`
		DatasetID string  `gorm:"column:dataset_id"`
		Fields    string  `gorm:"column:fields"`
		Distance  float64 `gorm:"column:distance"`
	}
	if err := r.db.WithContext(ctx).Raw(`
		SELECT di.id, di.dataset_id, di.fields, di.embedding <=> ? AS distance
		FROM dataset_items di
		JOIN datasets d ON d.id = di.dataset_id
		WHERE d.type = ? AND di.embedding IS NOT NULL
		ORDER BY distance
		LIMIT ?`, pgvector.NewVector(vec), typ, n).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]port.SimilarDatasetItem, len(rows))
	for i, row := range rows {
		out[i] = port.SimilarDatasetItem{
			DatasetItem: model.DatasetItem{ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields},
			Distance:    row.Distance,
		}
	}
	return out, nil
}

/* ---- 全文检索（FTS） ---- */

// SearchDatasetItemsFTS 全文检索：表达式 tsvector @@ websearch_to_tsquery，与
// DatasetIndexer 动态建的表达式 GIN 索引两侧共用同一分词配置与表达式形状
// （逐字一致才命中索引）。fields 为本数据集 schema 的 FTS 字段清单（多字段 OR，
// 各自可 BitmapOr 命中索引）；排序按合成 tsvector 的 ts_rank（仅作用于命中子集）。
func (r *DatasetRepo) SearchDatasetItemsFTS(ctx context.Context, datasetID string, fields []string, q string, n int) ([]model.DatasetItem, error) {
	if n <= 0 || n > 200 {
		n = 50
	}
	cfg := strings.ReplaceAll(r.tsConfig, "'", "")
	conds := make([]string, 0, len(fields))
	rankParts := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.Contains(f, "'") { // key 白名单上游已保证；此处纵深防御
			continue
		}
		conds = append(conds, fmt.Sprintf("to_tsvector('%s', fields ->> '%s') @@ websearch_to_tsquery('%s', ?)", cfg, f, cfg))
		rankParts = append(rankParts, fmt.Sprintf("coalesce(fields ->> '%s', '')", f))
	}
	if len(conds) == 0 {
		return nil, fmt.Errorf("该数据集没有可全文检索的字段（schema 未标记 fts）")
	}
	rankExpr := fmt.Sprintf("to_tsvector('%s', concat_ws(' ', %s))", cfg, strings.Join(rankParts, ", "))
	sql := `SELECT id, dataset_id, fields, item_key, fingerprint, source_task_id, created_at, updated_at
		FROM dataset_items
		WHERE dataset_id = ? AND (` + strings.Join(conds, " OR ") + `)
		ORDER BY ts_rank(` + rankExpr + `, websearch_to_tsquery('` + cfg + `', ?)) DESC
		LIMIT ?`
	args := make([]any, 0, len(conds)+3)
	args = append(args, datasetID)
	for range conds {
		args = append(args, q)
	}
	args = append(args, q, n)
	// 显式列投影（嵌套带 TableName 的行结构会被 GORM 当关联表丢列——archive_repo 老坑）
	var rows []struct {
		ID           string    `gorm:"column:id"`
		DatasetID    string    `gorm:"column:dataset_id"`
		Fields       string    `gorm:"column:fields"`
		ItemKey      string    `gorm:"column:item_key"`
		Fingerprint  string    `gorm:"column:fingerprint"`
		SourceTaskID *string   `gorm:"column:source_task_id"`
		CreatedAt    time.Time `gorm:"column:created_at"`
		UpdatedAt    time.Time `gorm:"column:updated_at"`
	}
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.DatasetItem, len(rows))
	for i, row := range rows {
		out[i] = model.DatasetItem{
			ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields,
			ItemKey: row.ItemKey, Fingerprint: row.Fingerprint,
			SourceTaskID: strVal(row.SourceTaskID),
			CreatedAt:    row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}

/* ---- 行 <-> 模型转换 ---- */

func datasetToRow(d *model.Dataset) *datasetRow {
	return &datasetRow{
		ID: d.ID, Type: d.Type, Name: d.Name, Schema: d.Schema,
		Description: d.Description, Tags: encodeTags(d.Tags),
		SourceTaskID: strPtr(d.SourceTaskID), Status: d.Status,
		ItemCount: d.ItemCount, SchemaVersion: d.SchemaVersion, Extra: d.Extra,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func datasetToModel(row *datasetRow) model.Dataset {
	return model.Dataset{
		ID: row.ID, Type: row.Type, Name: row.Name, Schema: row.Schema,
		Description: row.Description, Tags: decodeTags(row.Tags),
		SourceTaskID: strVal(row.SourceTaskID), Status: row.Status,
		ItemCount: row.ItemCount, SchemaVersion: row.SchemaVersion, Extra: row.Extra,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func datasetItemRowToModel(row *datasetItemRow) model.DatasetItem {
	return model.DatasetItem{
		ID: row.ID, DatasetID: row.DatasetID, Fields: row.Fields,
		ItemKey: row.ItemKey, Fingerprint: row.Fingerprint,
		SourceTaskID: strVal(row.SourceTaskID),
		CreatedAt:    row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// encodeTags/decodeTags 标签 <-> JSON 数组文本（tags 列为 TEXT）。
func encodeTags(tags []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, t := range tags {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + strings.ReplaceAll(t, `"`, `\"`) + `"`)
	}
	b.WriteByte(']')
	return b.String()
}

func decodeTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	var tags []string
	// 简单 JSON 字符串数组解析（写入侧 encodeTags 保证形状）
	_ = json.Unmarshal([]byte(s), &tags)
	return tags
}

func vectorPtr(v []float32) *pgvector.Vector {
	if len(v) == 0 {
		return nil
	}
	vec := pgvector.NewVector(v)
	return &vec
}
