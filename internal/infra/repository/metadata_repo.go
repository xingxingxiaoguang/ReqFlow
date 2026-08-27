package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/port"
)

// MetadataRepo 元数据注册表仓储（照 task_repo 模式：行结构 + 显式转换，
// Raw SQL 显式列映射——嵌套结构体 Scan 会丢列，dataset_repo 踩过的坑）。
type MetadataRepo struct{ db *gorm.DB }

func NewMetadataRepo(db *gorm.DB) *MetadataRepo { return &MetadataRepo{db: db} }

func (r *MetadataRepo) CreateEntry(ctx context.Context, e *port.MetadataEntry) error {
	if e.Version <= 0 {
		e.Version = 1
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	row := metadataRegistryRow{
		ID: uuid.NewString(), Kind: e.Kind, Key: e.Key, Version: e.Version,
		Payload: e.Payload, Enabled: e.Enabled, Summary: e.Summary,
		CreatedBy: e.CreatedBy, CreatedAt: e.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	e.CreatedAt = row.CreatedAt
	return nil
}

func (r *MetadataRepo) LatestEntries(ctx context.Context) ([]port.MetadataEntry, error) {
	// DISTINCT ON 取每 (kind,key) 的最大 version 行（含 disabled——是否生效由 app 判定）
	var rows []metadataRegistryRow
	if err := r.db.WithContext(ctx).Raw(
		`SELECT DISTINCT ON (kind, key) id, kind, key, version, payload, enabled, summary, created_by, created_at
		 FROM metadata_registry ORDER BY kind, key, version DESC`,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return registryRowsToEntries(rows), nil
}

func (r *MetadataRepo) ListVersions(ctx context.Context, kind, key string, limit int) ([]port.MetadataEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []metadataRegistryRow
	if err := r.db.WithContext(ctx).
		Where("kind = ? AND key = ?", kind, key).
		Order("version DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return registryRowsToEntries(rows), nil
}

func (r *MetadataRepo) WriteAudit(ctx context.Context, a *port.MetadataAuditEntry) error {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(&metadataAuditRow{
		ID: uuid.NewString(), Action: a.Action, Kind: a.Kind, Key: a.Key,
		FromVersion: a.FromVersion, ToVersion: a.ToVersion,
		Summary: a.Summary, Operator: a.Operator, CreatedAt: a.CreatedAt,
	}).Error
}

func (r *MetadataRepo) ListAudit(ctx context.Context, kind, key string, limit int) ([]port.MetadataAuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []metadataAuditRow
	if err := r.db.WithContext(ctx).
		Where("kind = ? AND key = ?", kind, key).
		Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]port.MetadataAuditEntry, len(rows))
	for i, row := range rows {
		out[i] = port.MetadataAuditEntry{
			Action: row.Action, Kind: row.Kind, Key: row.Key,
			FromVersion: row.FromVersion, ToVersion: row.ToVersion,
			Summary: row.Summary, Operator: row.Operator, CreatedAt: row.CreatedAt,
		}
	}
	return out, nil
}

func (r *MetadataRepo) UpdateLatestEnabled(ctx context.Context, kind, key string, enabled bool) error {
	// 子查询定位该 (kind,key) 最新版本行，单条 UPDATE 原地翻转发布标志（内容不动）
	res := r.db.WithContext(ctx).Exec(
		`UPDATE metadata_registry SET enabled = ?
		 WHERE id = (SELECT id FROM (SELECT id FROM metadata_registry
		            WHERE kind = ? AND key = ? ORDER BY version DESC LIMIT 1) latest)`,
		enabled, kind, key)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound // 该 key 无任何版本行可翻转
	}
	return nil
}

func registryRowsToEntries(rows []metadataRegistryRow) []port.MetadataEntry {
	out := make([]port.MetadataEntry, len(rows))
	for i, row := range rows {
		out[i] = port.MetadataEntry{
			Kind: row.Kind, Key: row.Key, Version: row.Version,
			Payload: row.Payload, Enabled: row.Enabled, Summary: row.Summary,
			CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		}
	}
	return out
}
