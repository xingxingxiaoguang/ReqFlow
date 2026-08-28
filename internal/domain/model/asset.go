package model

import "time"

// Asset 是内容不可变的原始输入。相同内容可以被多个 AssetSet 引用。
type Asset struct {
	ID          string
	WorkspaceID string
	Filename    string
	MIMEType    string
	SizeBytes   int64
	SHA256      string
	BlobURI     string
	CreatedAt   time.Time
}

// AssetSet 是一次业务输入选择的文件集合。
type AssetSet struct {
	ID          string
	WorkspaceID string
	Name        string
	CreatedBy   string
	CreatedAt   time.Time
}

// AssetSetMember 固化文件在集合中的顺序。
type AssetSetMember struct {
	AssetSetID string
	AssetID    string
	Ordinal    int
}

// ParsedDocument 是某个解析器版本对 Asset 的结构化解析结果。
type ParsedDocument struct {
	ID            string
	AssetID       string
	ParserName    string
	ParserVersion string
	Status        string
	ContentHash   string
	ErrorMessage  string
	CreatedAt     time.Time
}

const (
	ParsedDocumentPending   = "pending"
	ParsedDocumentRunning   = "running"
	ParsedDocumentSucceeded = "succeeded"
	ParsedDocumentFailed    = "failed"
)

// DocumentBlock 是可被抽取结果稳定引用的最小文档区块。
type DocumentBlock struct {
	ID               string
	ParsedDocumentID string
	Ordinal          int
	BlockType        string
	PageNo           int
	SectionPath      string
	Text             string
	Metadata         string
}

const (
	BlockHeading      = "heading"
	BlockParagraph    = "paragraph"
	BlockTable        = "table"
	BlockList         = "list"
	BlockImageCaption = "image_caption"
)
