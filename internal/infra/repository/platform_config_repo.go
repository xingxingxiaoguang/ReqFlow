package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type PlatformConfigRepo struct{ db *gorm.DB }

func NewPlatformConfigRepo(db *gorm.DB) *PlatformConfigRepo { return &PlatformConfigRepo{db: db} }

type platformConfigRow struct {
	ID               string    `gorm:"column:id;primaryKey"`
	WorkspaceID      string    `gorm:"column:workspace_id"`
	Kind             string    `gorm:"column:kind"`
	Name             string    `gorm:"column:name"`
	Settings         string    `gorm:"column:settings;type:jsonb"`
	SecretCiphertext string    `gorm:"column:secret_ciphertext"`
	Active           bool      `gorm:"column:is_active"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (platformConfigRow) TableName() string { return "platform_configs" }

func (r *PlatformConfigRepo) ListPlatformConfigs(ctx context.Context, workspaceID, kind string) ([]model.PlatformConfig, error) {
	var rows []platformConfigRow
	if err := r.db.WithContext(ctx).Where("workspace_id = ? AND kind = ?", workspaceID, kind).
		Order("is_active DESC, updated_at DESC, name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]model.PlatformConfig, len(rows))
	for i := range rows {
		out[i] = platformConfigToModel(rows[i])
	}
	return out, nil
}

func (r *PlatformConfigRepo) GetPlatformConfig(ctx context.Context, workspaceID, kind, id string) (*model.PlatformConfig, error) {
	var row platformConfigRow
	err := r.db.WithContext(ctx).Where("workspace_id = ? AND kind = ? AND id = ?", workspaceID, kind, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrPlatformConfigNotFound
	}
	if err != nil {
		return nil, err
	}
	value := platformConfigToModel(row)
	return &value, nil
}

func (r *PlatformConfigRepo) GetActivePlatformConfig(ctx context.Context, workspaceID, kind string) (*model.PlatformConfig, error) {
	var row platformConfigRow
	err := r.db.WithContext(ctx).Where("workspace_id = ? AND kind = ? AND is_active = TRUE", workspaceID, kind).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, port.ErrPlatformConfigNotFound
	}
	if err != nil {
		return nil, err
	}
	value := platformConfigToModel(row)
	return &value, nil
}

func (r *PlatformConfigRepo) CreatePlatformConfig(ctx context.Context, value *model.PlatformConfig) error {
	if value.ID == "" {
		value.ID = uuid.NewString()
	}
	now := time.Now()
	value.CreatedAt, value.UpdatedAt = now, now
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if value.Active {
			if err := lockPlatformConfigKind(tx, value.WorkspaceID, value.Kind); err != nil {
				return err
			}
			if err := tx.Model(&platformConfigRow{}).
				Where("workspace_id = ? AND kind = ? AND is_active = TRUE", value.WorkspaceID, value.Kind).
				Updates(map[string]any{"is_active": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return tx.Create(platformConfigFromModel(value)).Error
	})
}

func (r *PlatformConfigRepo) UpdatePlatformConfig(ctx context.Context, value *model.PlatformConfig) error {
	value.UpdatedAt = time.Now()
	result := r.db.WithContext(ctx).Model(&platformConfigRow{}).
		Where("workspace_id = ? AND kind = ? AND id = ?", value.WorkspaceID, value.Kind, value.ID).
		Updates(map[string]any{"name": value.Name, "settings": string(value.Settings),
			"secret_ciphertext": value.SecretCiphertext, "updated_at": value.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return port.ErrPlatformConfigNotFound
	}
	return nil
}

func (r *PlatformConfigRepo) DeletePlatformConfig(ctx context.Context, workspaceID, kind, id string) error {
	result := r.db.WithContext(ctx).Where("workspace_id = ? AND kind = ? AND id = ?", workspaceID, kind, id).
		Delete(&platformConfigRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return port.ErrPlatformConfigNotFound
	}
	return nil
}

func (r *PlatformConfigRepo) ActivatePlatformConfig(ctx context.Context, workspaceID, kind, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockPlatformConfigKind(tx, workspaceID, kind); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&platformConfigRow{}).
			Where("workspace_id = ? AND kind = ? AND id = ?", workspaceID, kind, id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return port.ErrPlatformConfigNotFound
		}
		now := time.Now()
		if err := tx.Model(&platformConfigRow{}).Where("workspace_id = ? AND kind = ? AND is_active = TRUE", workspaceID, kind).
			Updates(map[string]any{"is_active": false, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&platformConfigRow{}).Where("workspace_id = ? AND kind = ? AND id = ?", workspaceID, kind, id).
			Updates(map[string]any{"is_active": true, "updated_at": now}).Error
	})
}

func (r *PlatformConfigRepo) DeactivatePlatformConfigs(ctx context.Context, workspaceID, kind string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockPlatformConfigKind(tx, workspaceID, kind); err != nil {
			return err
		}
		return tx.Model(&platformConfigRow{}).Where("workspace_id = ? AND kind = ? AND is_active = TRUE", workspaceID, kind).
			Updates(map[string]any{"is_active": false, "updated_at": time.Now()}).Error
	})
}

func lockPlatformConfigKind(tx *gorm.DB, workspaceID, kind string) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", workspaceID+"/platform-config/"+kind).Error
}

func platformConfigFromModel(value *model.PlatformConfig) *platformConfigRow {
	return &platformConfigRow{ID: value.ID, WorkspaceID: value.WorkspaceID, Kind: value.Kind,
		Name: value.Name, Settings: string(value.Settings), SecretCiphertext: value.SecretCiphertext,
		Active: value.Active, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func platformConfigToModel(row platformConfigRow) model.PlatformConfig {
	return model.PlatformConfig{ID: row.ID, WorkspaceID: row.WorkspaceID, Kind: row.Kind,
		Name: row.Name, Settings: []byte(row.Settings), SecretCiphertext: row.SecretCiphertext,
		Active: row.Active, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
