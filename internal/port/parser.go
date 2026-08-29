package port

import (
	"context"
	"io"

	"reqflow/internal/domain/model"
)

// ParseProgress 文档解析进度（云端解析轮询期间周期回调）。
type ParseProgress struct {
	Message    string
	ElapsedSec int
}

// ParseSource 是与 BlobStore 实现无关的解析输入。Parser 不能依赖本地路径，
// 因而未来从本地文件切换到 S3/MinIO 时不需要改变业务契约。
type ParseSource struct {
	Filename  string
	MIMEType  string
	SizeBytes int64
	Content   io.Reader
}

// DocumentParser 返回可稳定引用的结构化区块，而不是整篇 string。
// ParserName + ParserVersion 共同构成解析缓存键；改变解析语义必须提升版本。
type DocumentParser interface {
	ParserName() string
	ParserVersion() string
	Parse(ctx context.Context, source ParseSource, onProgress func(ParseProgress)) ([]model.DocumentBlock, error)
}
