package port

import (
	"context"
	"errors"
	"time"

	"reqflow/internal/domain/model"
)

var ErrPlatformConfigNotFound = errors.New("platform config not found")

type LLMRuntimeConfig struct {
	Provider    string  `json:"provider"`
	BaseURL     string  `json:"base_url"`
	APIKey      string  `json:"-"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	TimeoutMs   int     `json:"timeout_ms"`
}

type EmbeddingRuntimeConfig struct {
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"-"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
	BatchSize  int    `json:"batch_size"`
	TimeoutMs  int    `json:"timeout_ms"`
}

type RerankRuntimeConfig struct {
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"-"`
	Model     string `json:"model"`
	TimeoutMs int    `json:"timeout_ms"`
}

type MinerURuntimeConfig struct {
	Enabled        bool   `json:"enabled"`
	APIURL         string `json:"api_url"`
	APIToken       string `json:"-"`
	ModelVersion   string `json:"model_version"`
	TimeoutMs      int    `json:"timeout_ms"`
	PollIntervalMs int    `json:"poll_interval_ms"`
}

// PlatformConfigResolver 是运行时唯一的配置入口：数据库激活项优先，否则返回文件兜底。
type PlatformConfigResolver interface {
	ResolveLLM(ctx context.Context) (LLMRuntimeConfig, error)
	ResolveEmbedding(ctx context.Context) (EmbeddingRuntimeConfig, error)
	ResolveRerank(ctx context.Context) (RerankRuntimeConfig, error)
	ResolveMinerU(ctx context.Context) (MinerURuntimeConfig, error)
}

type PlatformConfigRepo interface {
	ListPlatformConfigs(ctx context.Context, workspaceID, kind string) ([]model.PlatformConfig, error)
	GetPlatformConfig(ctx context.Context, workspaceID, kind, id string) (*model.PlatformConfig, error)
	GetActivePlatformConfig(ctx context.Context, workspaceID, kind string) (*model.PlatformConfig, error)
	CreatePlatformConfig(ctx context.Context, value *model.PlatformConfig) error
	UpdatePlatformConfig(ctx context.Context, value *model.PlatformConfig) error
	DeletePlatformConfig(ctx context.Context, workspaceID, kind, id string) error
	ActivatePlatformConfig(ctx context.Context, workspaceID, kind, id string) error
	DeactivatePlatformConfigs(ctx context.Context, workspaceID, kind string) error
}

type SecretCipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// RuntimeConfigSnapshot 便于执行阶段记录真正生效的模型，而不是进程启动时的旧值。
type RuntimeConfigSnapshot struct {
	Model     string
	BatchSize int
	LoadedAt  time.Time
}
