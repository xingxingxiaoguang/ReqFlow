// 归档仓储：主表 ↔ 归档表的 SQL 直搬（同构表 INSERT ... SELECT，向量零序列化损耗）。
// 每次搬移在单事务内完成（主表删除与归档落位互斥成立，不会丢数据）。
// 读取一律 Raw+Scan 投影：嵌入 taskRow/datasetRow（自带 TableName）会被 GORM 当
// 关联表处理导致列丢失——dataset_repo 的老坑，不重蹈。
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ArchiveRepo 归档仓储（构造于 *gorm.DB）。
type ArchiveRepo struct{ db *gorm.DB }

func NewArchiveRepo(db *gorm.DB) *ArchiveRepo { return &ArchiveRepo{db: db} }

/* ---- 任务归档 ---- */

func (r *ArchiveRepo) ArchiveTask(ctx context.Context, task *model.Task, snap port.TaskStepsSnapshot) error {
	stepsJSON, err := json.Marshal(snap.Steps)
	if err != nil {
		return fmt.Errorf("步骤快照序列化失败: %w", err)
	}
	itemsJSON, err := json.Marshal(snap.Items)
	if err != nil {
		return fmt.Errorf("明细快照序列化失败: %w", err)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// archived_tasks 与 tasks 同构（尾部追加 3 列），SELECT t.*, 3 值按列序对齐
		res := tx.Exec(`INSERT INTO archived_tasks
			SELECT t.*, ?, ?, now() FROM tasks t WHERE t.id = ?`,
			string(stepsJSON), string(itemsJSON), task.ID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("任务不存在或已归档")
		}
		// task_steps/task_items 有 ON DELETE CASCADE，随主行一并移出主表
		return tx.Exec(`DELETE FROM tasks WHERE id = ?`, task.ID).Error
	})
}

func (r *ArchiveRepo) RestoreTask(ctx context.Context, taskID string) (*model.Task, port.TaskStepsSnapshot, error) {
	var trow taskRow
	if err := r.db.WithContext(ctx).Raw(`SELECT * FROM archived_tasks WHERE id = ?`, taskID).Scan(&trow).Error; err != nil {
		return nil, port.TaskStepsSnapshot{}, err
	}
	if trow.ID == "" {
		return nil, port.TaskStepsSnapshot{}, fmt.Errorf("归档中不存在该任务")
	}
	var snapRow struct {
		StepsSnapshot string `gorm:"column:steps_snapshot"`
		ItemsSnapshot string `gorm:"column:items_snapshot"`
	}
	if err := r.db.WithContext(ctx).
		Raw(`SELECT steps_snapshot, items_snapshot FROM archived_tasks WHERE id = ?`, taskID).
		Scan(&snapRow).Error; err != nil {
		return nil, port.TaskStepsSnapshot{}, err
	}
	task := taskToModel(&trow)
	snap := port.TaskStepsSnapshot{}
	_ = json.Unmarshal([]byte(snapRow.StepsSnapshot), &snap.Steps)
	_ = json.Unmarshal([]byte(snapRow.ItemsSnapshot), &snap.Items)

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(taskToRow(&task)).Error; err != nil {
			return fmt.Errorf("任务写回失败（ID 可能已存在）: %w", err)
		}
		if len(snap.Steps) > 0 {
			rows := make([]taskStepRow, len(snap.Steps))
			for i, s := range snap.Steps {
				rows[i] = stepToRow(s, taskID)
			}
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("步骤写回失败: %w", err)
			}
		}
		if len(snap.Items) > 0 {
			rows := make([]taskItemRow, len(snap.Items))
			for i, it := range snap.Items {
				rows[i] = itemToRow(it.ID, taskID, it)
			}
			if err := tx.Create(&rows).Error; err != nil {
				return fmt.Errorf("明细写回失败: %w", err)
			}
		}
		return tx.Exec(`DELETE FROM archived_tasks WHERE id = ?`, taskID).Error
	})
	if err != nil {
		return nil, port.TaskStepsSnapshot{}, err
	}
	return &task, snap, nil
}

func (r *ArchiveRepo) ListArchivedTasks(ctx context.Context, typ string, limit int) ([]model.ArchivedTask, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// 列表投影：完全平铺（嵌入带 TableName 的行结构会被 GORM 当关联丢列）
	var rows []struct {
		ID              string    `gorm:"column:id"`
		Type            string    `gorm:"column:type"`
		Title           string    `gorm:"column:title"`
		Status          string    `gorm:"column:status"`
		CurrentStep     int       `gorm:"column:current_step"`
		ItemsCount      int       `gorm:"column:items_count"`
		OutputDatasetID *string   `gorm:"column:output_dataset_id"`
		CreatedAt       time.Time `gorm:"column:created_at"`
		ArchivedAt      time.Time `gorm:"column:archived_at"`
	}
	sql, args := archivedListSQL("archived_tasks", `id, type, title, status, current_step,
		items_count, output_dataset_id, created_at`, typ, limit)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ArchivedTask, len(rows))
	for i, row := range rows {
		out[i] = model.ArchivedTask{
			Task: model.Task{
				ID: row.ID, Type: row.Type, Title: row.Title, Status: row.Status,
				CurrentStep: row.CurrentStep, ItemsCount: row.ItemsCount,
				OutputDatasetID: strVal(row.OutputDatasetID), CreatedAt: row.CreatedAt,
			},
			ArchivedAt: row.ArchivedAt,
		}
	}
	return out, nil
}

/* ---- 数据集归档 ---- */

func (r *ArchiveRepo) ArchiveDataset(ctx context.Context, datasetID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`INSERT INTO archived_datasets
			SELECT d.*, now() FROM datasets d WHERE d.id = ?`, datasetID)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("数据集不存在或已归档")
		}
		// 条目含向量直搬（embedding 原生保留，恢复后语义检索即可用）
		if err := tx.Exec(`INSERT INTO archived_dataset_items
			SELECT di.*, now() FROM dataset_items di WHERE di.dataset_id = ?`, datasetID).Error; err != nil {
			return err
		}
		// dataset_items 有 ON DELETE CASCADE，删除数据集行时一并移出主表
		return tx.Exec(`DELETE FROM datasets WHERE id = ?`, datasetID).Error
	})
}

// 归档表比主表多 archived_at 列，恢复方向的 SELECT 必须显式列名（不能用 *）。
const datasetCols = `id, type, name, schema, description, tags, source_task_id, status,
	item_count, schema_version, extra, created_at, updated_at`
const datasetItemCols = `id, dataset_id, fields, item_key, fingerprint, metadata,
	source_task_id, embedding, created_at, updated_at`

func (r *ArchiveRepo) RestoreDataset(ctx context.Context, datasetID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`INSERT INTO datasets (`+datasetCols+`)
			SELECT `+datasetCols+` FROM archived_datasets WHERE id = ?`, datasetID)
		if res.Error != nil {
			return fmt.Errorf("数据集写回失败（ID 可能已存在）: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("归档中不存在该数据集")
		}
		if err := tx.Exec(`INSERT INTO dataset_items (`+datasetItemCols+`)
			SELECT `+datasetItemCols+` FROM archived_dataset_items WHERE dataset_id = ?`, datasetID).Error; err != nil {
			return fmt.Errorf("条目写回失败: %w", err)
		}
		// 条目归档行随头行一并清理（否则重复归档会累积残留）
		if err := tx.Exec(`DELETE FROM archived_dataset_items WHERE dataset_id = ?`, datasetID).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM archived_datasets WHERE id = ?`, datasetID).Error
	})
}

func (r *ArchiveRepo) ListArchivedDatasets(ctx context.Context, typ string, limit int) ([]model.ArchivedDataset, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []struct {
		ID          string    `gorm:"column:id"`
		Type        string    `gorm:"column:type"`
		Name        string    `gorm:"column:name"`
		Description string    `gorm:"column:description"`
		Tags        string    `gorm:"column:tags"`
		Status      string    `gorm:"column:status"`
		ItemCount   int       `gorm:"column:item_count"`
		CreatedAt   time.Time `gorm:"column:created_at"`
		ArchivedAt  time.Time `gorm:"column:archived_at"`
	}
	sql, args := archivedListSQL("archived_datasets", `id, type, name, description, tags,
		status, item_count, created_at`, typ, limit)
	if err := r.db.WithContext(ctx).Raw(sql, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.ArchivedDataset, len(rows))
	for i, row := range rows {
		out[i] = model.ArchivedDataset{
			Dataset: model.Dataset{
				ID: row.ID, Type: row.Type, Name: row.Name, Description: row.Description,
				Tags: decodeTags(row.Tags), Status: row.Status, ItemCount: row.ItemCount,
				CreatedAt: row.CreatedAt,
			},
			ArchivedAt: row.ArchivedAt,
		}
	}
	return out, nil
}

// archivedListSQL 组装归档列表 Raw SQL（cols + archived_at，按类型过滤，时间倒序）。
func archivedListSQL(table, cols, typ string, limit int) (string, []any) {
	sql := "SELECT " + cols + ", archived_at FROM " + table
	var args []any
	if typ != "" {
		sql += " WHERE type = ?"
		args = append(args, typ)
	}
	sql += " ORDER BY archived_at DESC LIMIT ?"
	args = append(args, limit)
	return sql, args
}
