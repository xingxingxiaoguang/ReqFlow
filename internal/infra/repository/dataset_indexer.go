// 数据集动态索引管理器：字段定义在数据集行上（datasets.schema），FTS/筛选索引是
// schema 的派生物——非迁移资产，生命周期 = 数据集生命周期。
// 实现为 PG 表达式索引：字段仍是 JSONB 字段袋，索引直接建在 (fields->>'key') 与
// to_tsvector(cfg, fields->>'key') 表达式上，并带 dataset_id 部分谓词只覆盖本数据集。
// 索引名由 (datasetID, key, 用途) 确定性哈希派生，Sync 按 diff 只建删必要项；
// FTS 索引额外比对定义中的分词配置（config 变更时重建，保证与查询表达式逐字一致）。
// 不用 CONCURRENTLY：研发规模下秒级完成，且 CONCURRENTLY 不能在事务内执行。
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
)

const dsIndexPrefix = "dsidx_"

type DatasetIndexer struct {
	db       *gorm.DB
	tsConfig string
}

// NewDatasetIndexer 构造索引管理器（tsConfig 为 PG 文本搜索配置标识，空回退 simple）。
func NewDatasetIndexer(db *gorm.DB, tsConfig string) *DatasetIndexer {
	if strings.TrimSpace(tsConfig) == "" {
		tsConfig = "simple"
	}
	return &DatasetIndexer{db: db, tsConfig: tsConfig}
}

// SyncIndexes 按 schema 同步某数据集的动态索引：FTS 字段 → 表达式 GIN；
// Filterable 字段 → 表达式 btree。形状非法的 key 跳过（不阻塞写入——写路径上游
// 已有 logic.IsValidIdentifier 白名单，此处只作纵深防御）。
func (r *DatasetIndexer) SyncIndexes(ctx context.Context, datasetID string, schema model.DatasetSchema) error {
	want := planIndexes(datasetID, schema, r.tsConfig)

	existing, err := r.listIndexes(ctx, datasetID)
	if err != nil {
		return err
	}
	for name, def := range existing {
		keep := false
		if _, ok := want[name]; ok {
			keep = true
			// FTS 索引名相同但分词配置已变（def 与当前配置不一致）→ 重建
			if strings.Contains(def, "to_tsvector") && !strings.Contains(def, "to_tsvector('"+r.tsConfig+"'") {
				keep = false
			}
		}
		if keep {
			delete(want, name) // 已正确存在，跳过创建
			continue
		}
		if err := r.db.WithContext(ctx).Exec(`DROP INDEX IF EXISTS ` + name).Error; err != nil {
			return fmt.Errorf("删除动态索引 %s 失败: %w", name, err)
		}
	}
	for name, ddl := range want {
		if err := r.db.WithContext(ctx).Exec(ddl).Error; err != nil {
			return fmt.Errorf("创建动态索引 %s 失败: %w", name, err)
		}
	}
	return nil
}

// DropIndexes 删除某数据集的全部动态索引（归档时回收；幂等）。
func (r *DatasetIndexer) DropIndexes(ctx context.Context, datasetID string) error {
	existing, err := r.listIndexes(ctx, datasetID)
	if err != nil {
		return err
	}
	for name := range existing {
		if err := r.db.WithContext(ctx).Exec(`DROP INDEX IF EXISTS ` + name).Error; err != nil {
			return fmt.Errorf("删除动态索引 %s 失败: %w", name, err)
		}
	}
	return nil
}

// listIndexes 列出 dataset_items 上属于该数据集的动态索引（名 → 定义）。
// 动态索引一律带 dataset_id 谓词，按定义内容匹配（pg_indexes LIKE，规模内开销可忽略）。
func (r *DatasetIndexer) listIndexes(ctx context.Context, datasetID string) (map[string]string, error) {
	var rows []struct {
		Indexname string `gorm:"column:indexname"`
		Indexdef  string `gorm:"column:indexdef"`
	}
	if err := r.db.WithContext(ctx).Raw(
		`SELECT indexname, indexdef FROM pg_indexes
		 WHERE schemaname = current_schema() AND tablename = 'dataset_items'
		   AND indexname LIKE '`+dsIndexPrefix+`%'
		   AND indexdef LIKE ?`, "%"+datasetID+"%").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Indexname] = row.Indexdef
	}
	return out, nil
}

// planIndexes 计算期望的动态索引集合（索引名 → DDL）——纯函数，单测钉住表达式
// 形状（索引命中要求与查询表达式逐字一致，形状回归即测试失败）。
func planIndexes(datasetID string, schema model.DatasetSchema, tsConfig string) map[string]string {
	id := literal(datasetID)
	want := map[string]string{}
	for _, f := range schema.Fields {
		if !logic.IsValidIdentifier(f.Key) {
			continue
		}
		key := literal(f.Key)
		if f.FTS {
			name := dsIndexName(datasetID, f.Key, "fts")
			want[name] = fmt.Sprintf(
				`CREATE INDEX IF NOT EXISTS %s ON dataset_items USING gin (to_tsvector('%s', fields ->> '%s')) WHERE dataset_id = '%s'`,
				name, literal(tsConfig), key, id)
		}
		if f.Filterable {
			name := dsIndexName(datasetID, f.Key, "flt")
			want[name] = fmt.Sprintf(
				`CREATE INDEX IF NOT EXISTS %s ON dataset_items ((fields ->> '%s')) WHERE dataset_id = '%s'`,
				name, key, id)
		}
	}
	return want
}

// dsIndexName 动态索引确定性命名：dsidx_<sha256(datasetID|key) 前 12 位>_<用途>。
// 同 (数据集, 字段, 用途) 恒同名，Sync 才能按名 diff。
func dsIndexName(datasetID, key, kind string) string {
	sum := sha256.Sum256([]byte(datasetID + "|" + key))
	return dsIndexPrefix + hex.EncodeToString(sum[:])[:12] + "_" + kind
}

// literal SQL 字面量纵深防御（上游已校验；剥离单引号杜绝拼接逃逸）。
func literal(s string) string {
	return strings.ReplaceAll(s, "'", "")
}
