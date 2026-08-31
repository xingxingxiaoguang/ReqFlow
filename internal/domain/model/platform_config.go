package model

import (
	"encoding/json"
	"time"
)

const (
	PlatformConfigLLM       = "llm"
	PlatformConfigEmbedding = "embedding"
	PlatformConfigRerank    = "rerank"
	PlatformConfigMinerU    = "mineru"
)

// PlatformConfig 是数据库持久化的外部能力配置。配置文件兜底项只在应用层合成，
// 不写入数据库；SecretCiphertext 也绝不进入 HTTP 视图。
type PlatformConfig struct {
	ID               string
	WorkspaceID      string
	Kind             string
	Name             string
	Settings         json.RawMessage
	SecretCiphertext string
	Active           bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
