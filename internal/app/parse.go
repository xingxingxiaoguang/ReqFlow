package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"reqflow/internal/port"
)

// ParseProgress 文档解析进度（app 层形状，port 类型不外泄）。
type ParseProgress struct {
	Message    string
	ElapsedSec int
}

// ParseService 文档解析用例：解析确认门的上半段（解析 → 全文返回前端预览/编辑）。
type ParseService struct {
	parser port.DocumentParser
}

// NewParseService 构造用例。
func NewParseService(parser port.DocumentParser) *ParseService {
	return &ParseService{parser: parser}
}

// Run 解析文档，返回全文文本；onProgress 在云端解析轮询期间周期回调。
func (s *ParseService) Run(ctx context.Context, filename, filePath string, onProgress func(ParseProgress)) (string, error) {
	file, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		return "", err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	blocks, err := s.parser.Parse(ctx, port.ParseSource{Filename: filename, SizeBytes: stat.Size(), Content: file}, func(p port.ParseProgress) {
		if onProgress != nil {
			onProgress(ParseProgress{Message: p.Message, ElapsedSec: p.ElapsedSec})
		}
	})
	if err != nil {
		return "", err
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n"), nil
}
