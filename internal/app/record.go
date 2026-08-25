package app

import (
	"context"
	"fmt"
	"os"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// RecordService 导入记录用例：列表、明细、原文查看（前端「恢复」即读取明细回填编辑态）。
type RecordService struct {
	records port.ImportRepo
}

// NewRecordService 构造用例。
func NewRecordService(records port.ImportRepo) *RecordService {
	return &RecordService{records: records}
}

// List 最近记录。
func (s *RecordService) List(ctx context.Context, limit int) ([]model.ImportRecord, error) {
	return s.records.ListRecords(ctx, limit)
}

// Get 记录 + 全部明细。
func (s *RecordService) Get(ctx context.Context, id string) (*model.ImportRecord, []model.ImportRecordItem, error) {
	rec, err := s.records.GetRecord(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	items, err := s.records.GetRecordItems(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return rec, items, nil
}

// Source 读取该记录的分析原文（存档文件）。
func (s *RecordService) Source(ctx context.Context, id string) (string, error) {
	rec, err := s.records.GetRecord(ctx, id)
	if err != nil {
		return "", err
	}
	if rec.OriginalFilePath == "" {
		return "", fmt.Errorf("该记录没有原文存档")
	}
	data, err := os.ReadFile(rec.OriginalFilePath)
	if err != nil {
		return "", fmt.Errorf("读取原文失败: %w", err)
	}
	return string(data), nil
}
