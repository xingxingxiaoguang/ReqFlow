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

// AssetSetEntry 是带资产元数据的集合读模型。AssetSetMember 仍是持久化关系，
// Entry 用于应用服务按固定顺序读取集合，避免调用方自行拼接两次查询。
type AssetSetEntry struct {
	Member AssetSetMember
	Asset  Asset
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

// ParsedDocumentSet 是 source.parse 对一次 AssetSet 的不可变逻辑输出。
// 单个文件失败只体现在成员状态上；下游读取这个 Manifest，而不是猜测某个
// AssetSet 当前“最新”的解析结果。ProducerAttempt 用于阻止过期 Worker 覆盖重试结果。
type ParsedDocumentSet struct {
	ID              string
	AssetSetID      string
	SourceStepRunID string
	ParserName      string
	ParserVersion   string
	Status          string
	ProducerAttempt int
	TotalCount      int
	SucceededCount  int
	FailedCount     int
	CreatedAt       time.Time
	FinishedAt      time.Time
}

const (
	ParsedDocumentSetRunning   = "running"
	ParsedDocumentSetSucceeded = "succeeded"
	ParsedDocumentSetPartial   = "partial"
	ParsedDocumentSetFailed    = "failed"
)

// ParsedDocumentSetItem 固化输入顺序和逐文件结果。失败项保留 AssetID 与错误，
// 后续重试可只重跑失败文件；成功项稳定引用 ParsedDocument。
type ParsedDocumentSetItem struct {
	ParsedDocumentSetID string
	AssetID             string
	ParsedDocumentID    string
	Ordinal             int
	Status              string
	ErrorMessage        string
}

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
