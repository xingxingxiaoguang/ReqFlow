// 数据集管理与字段定义受控编辑用例。
// 字段定义归属数据集实例（datasets.schema 为唯一真相源）：创建（从类型模板带出，
// 可自定义）→ 受控编辑（形状校验 → 兼容守卫 → 落库 + 审计 + 动态索引同步）。
// 类型级注册（app/registry.go 的 schema 覆盖层）降级为「数据集类型模板」，仅服务
// 本文件的模板带出——任务流程不再经任务类型解析字段定义。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// DatasetAdminService 数据集管理用例（创建/字段定义受控编辑）。
type DatasetAdminService struct {
	datasets port.DatasetRepo
	indexer  port.DatasetIndexer // 动态索引随 schema 同步（nil = 跳过，单测用）
	audit    port.MetadataRepo   // nil = 不记审计（单测用）
}

// NewDatasetAdminService 构造用例。
func NewDatasetAdminService(datasets port.DatasetRepo, indexer port.DatasetIndexer, audit port.MetadataRepo) *DatasetAdminService {
	return &DatasetAdminService{datasets: datasets, indexer: indexer, audit: audit}
}

// CreateDatasetInput 新建数据集入参（字段定义缺省从类型模板带出，可整体自定义）。
type CreateDatasetInput struct {
	Name        string               `json:"name"`
	Type        string               `json:"type"`                  // 模板类型标识（新建后即为本数据集的 type 标识）
	Description string               `json:"description,omitempty"` // 人类可读说明
	Tags        []string             `json:"tags,omitempty"`
	Schema      *model.DatasetSchema `json:"schema,omitempty"` // 缺省 = 类型模板；提供时必须合法
}

// CreateDataset 创建数据集：字段定义在创建时固化到数据集行（快照），实例可独立受控
// 演进——同类型的各数据集从此可各自演进字段。建为 ready 空数据集（创建任务时即可绑定）。
func (s *DatasetAdminService) CreateDataset(ctx context.Context, in CreateDatasetInput) (*model.Dataset, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, fmt.Errorf("数据集需要命名")
	}
	in.Type = strings.TrimSpace(in.Type)
	if !logic.IsValidIdentifier(in.Type) {
		return nil, fmt.Errorf("数据集类型 %q 非法（须为小写字母开头的 snake_case，≤63 字符）", in.Type)
	}
	schema, fromTemplate := model.DatasetSchema{}, false
	if in.Schema == nil {
		sc, ok := effectiveSchemaOf(in.Type)
		if !ok {
			return nil, fmt.Errorf("未注册的数据集类型模板 %s：请在元数据页注册模板，或直接提供字段定义", in.Type)
		}
		schema, fromTemplate = sc, true
	} else {
		schema = *in.Schema
	}
	schema.Type = in.Type // 类型一致性由服务端强制
	if err := logic.ValidateSchemaShape(schema); err != nil {
		return nil, err
	}
	if schema.Version <= 0 {
		schema.Version = 1
	}

	ds := &model.Dataset{
		Type: in.Type, Name: in.Name, Description: in.Description, Tags: in.Tags,
		Status: model.DatasetStatusReady, ItemCount: 0,
		SchemaVersion: schema.Version, Schema: marshalJSON(schema),
	}
	if err := s.datasets.CreateDataset(ctx, ds); err != nil {
		return nil, err
	}
	if s.indexer != nil {
		if err := s.indexer.SyncIndexes(ctx, ds.ID, schema); err != nil {
			return nil, err
		}
	}
	src := "自定义字段定义"
	if fromTemplate {
		src = "字段定义从类型模板带出"
	}
	slog.Info("数据集已创建", "name", ds.Name, "type", ds.Type, "fields", len(schema.Fields), "source", src)
	return ds, nil
}

// DatasetSchemaUpdateInput 数据集字段定义编辑入参（HTTP 绑定形状）。
type DatasetSchemaUpdateInput struct {
	Schema       model.DatasetSchema `json:"schema"`
	ConfirmRisky bool                `json:"confirm_risky"` // ⚠️ 项的显式确认
	Summary      string              `json:"summary"`       // 变更说明（审计）
}

// CheckDatasetSchema 字段定义变更 dry-run（与保存同一判定口径；不落库）。
// 空数据集（无条目）自由编辑：跳过兼容判定，只做形状校验——守卫保护的是存量数据，
// 不是限制字段设计。
func (s *DatasetAdminService) CheckDatasetSchema(ctx context.Context, datasetID string, in DatasetSchemaUpdateInput) (logic.CompatReport, error) {
	report, _, err := s.checkDatasetSchema(ctx, datasetID, in.Schema)
	return report, err
}

// UpdateDatasetSchema 数据集字段定义受控编辑：形状校验 → 兼容判定（空数据集跳过）
// → ❌ 拦截 / ⚠️ 需 confirm_risky → 落库（schema_version 递增）→ 审计 → 动态索引同步。
// NeedsReembed 只告警不自动重嵌（存量条目在下次内容更新时自动按新口径重嵌，与
// 类型级受控编辑同口径）。
func (s *DatasetAdminService) UpdateDatasetSchema(ctx context.Context, datasetID string,
	in DatasetSchemaUpdateInput) (logic.CompatReport, error) {
	report, ds, err := s.checkDatasetSchema(ctx, datasetID, in.Schema)
	if err != nil {
		return report, err
	}
	if report.Blocked {
		return report, fmt.Errorf("数据集已有 %d 条条目，字段定义存在不兼容变更（会打乱条目去重与展示），已拦截", ds.ItemCount)
	}
	if report.NeedsConfirm && !in.ConfirmRisky {
		return report, fmt.Errorf("存在风险变更（需显式确认）")
	}
	newSchema := in.Schema
	newSchema.Type = ds.Type // 类型一致性服务端强制
	newSchema.Version = ds.SchemaVersion + 1
	payload := marshalJSON(newSchema)
	if err := s.datasets.UpdateDatasetSchema(ctx, datasetID, payload, newSchema.Version); err != nil {
		return report, err
	}
	if s.indexer != nil {
		if err := s.indexer.SyncIndexes(ctx, datasetID, newSchema); err != nil {
			return report, err
		}
	}
	s.writeAudit(ctx, datasetID, ds.SchemaVersion, newSchema.Version,
		orDefault(in.Summary, "[数据集] 字段定义更新 "+summarizeReport(report)))
	return report, nil
}

/* ---- 私有 ---- */

// checkDatasetSchema 编辑共检：形状校验 + 兼容判定（dry-run 与保存共用的唯一口径）。
// 空数据集（ItemCount == 0）自由编辑——守卫保护存量条目的条目身份（InKey 决定
// item_key 去重）与可见性，没有条目就没有保护对象，增删改全部放行。
func (s *DatasetAdminService) checkDatasetSchema(ctx context.Context, datasetID string, in model.DatasetSchema) (logic.CompatReport, *model.Dataset, error) {
	ds, err := s.datasets.GetDataset(ctx, datasetID)
	if err != nil {
		return logic.CompatReport{}, nil, fmt.Errorf("数据集不存在: %w", err)
	}
	old, ok := model.ParseDatasetSchema(ds.Schema)
	if !ok {
		return logic.CompatReport{}, nil, fmt.Errorf("数据集「%s」字段定义缺失，无法受控编辑", ds.Name)
	}
	newSchema, err := s.normalizeSchemaUpdate(ctx, datasetID, in)
	if err != nil {
		return logic.CompatReport{}, nil, err
	}
	if ds.ItemCount == 0 {
		return logic.CompatReport{Findings: []logic.CompatFinding{{
			Level: logic.CompatOK, Rule: "empty_dataset_free_edit",
			Message: "空数据集：字段定义自由编辑（兼容守卫自有条目后生效，保护 item_key 去重与存量可见性）",
		}}}, ds, nil
	}
	return logic.CheckSchemaCompat(old, newSchema), ds, nil
}

/* ---- 私有 ---- */

// currentSchema 数据集当前生效的字段定义。
func (s *DatasetAdminService) currentSchema(ctx context.Context, datasetID string) (model.DatasetSchema, error) {
	ds, err := s.datasets.GetDataset(ctx, datasetID)
	if err != nil {
		return model.DatasetSchema{}, fmt.Errorf("数据集不存在: %w", err)
	}
	schema, ok := model.ParseDatasetSchema(ds.Schema)
	if !ok {
		return model.DatasetSchema{}, fmt.Errorf("数据集「%s」字段定义缺失", ds.Name)
	}
	return schema, nil
}

// normalizeSchemaUpdate 编辑入参整形：Type 强制回数据集自身（禁止借编辑换类型）+ 形状校验。
func (s *DatasetAdminService) normalizeSchemaUpdate(ctx context.Context, datasetID string, in model.DatasetSchema) (model.DatasetSchema, error) {
	ds, err := s.datasets.GetDataset(ctx, datasetID)
	if err != nil {
		return model.DatasetSchema{}, fmt.Errorf("数据集不存在: %w", err)
	}
	in.Type = ds.Type
	in.Version = 0 // 版本由服务端递增，不信任调用方
	if err := logic.ValidateSchemaShape(in); err != nil {
		return model.DatasetSchema{}, err
	}
	return in, nil
}

// writeAudit 数据集字段定义变更审计（失败只记日志不回滚业务写，与元数据审计同口径）。
func (s *DatasetAdminService) writeAudit(ctx context.Context, datasetID string, from, to int, summary string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.WriteAudit(ctx, &port.MetadataAuditEntry{
		Action: "update_dataset_schema", Kind: port.MetadataKindDatasetSchema, Key: datasetID,
		FromVersion: from, ToVersion: to, Summary: summary,
	}); err != nil {
		slog.Warn("数据集审计写入失败", "dataset", datasetID, "err", err)
	}
}
