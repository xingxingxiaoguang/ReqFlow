package port

import "context"

// ParseProgress 文档解析进度（云端解析轮询期间周期回调）。
type ParseProgress struct {
	Message    string
	ElapsedSec int
}

// DocParser 文档解析器：按文件名后缀分发，返回提取的全文文本。
// 第一波支持 txt/md/docx/pdf；xlsx 行级解析能力已就绪于 infra，
// 第二波 bug Excel 导入在同一契约上开放。
type DocParser interface {
	Parse(ctx context.Context, filename, filePath string, onProgress func(ParseProgress)) (string, error)
}
