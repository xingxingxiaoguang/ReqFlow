package platformconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	defaultWorkspaceID = "default"
	fileConfigID       = "file"
)

var kinds = []string{
	model.PlatformConfigLLM,
	model.PlatformConfigEmbedding,
	model.PlatformConfigRerank,
	model.PlatformConfigMinerU,
}

type Fallbacks struct {
	WorkspaceName string
	LLM           port.LLMRuntimeConfig
	Embedding     port.EmbeddingRuntimeConfig
	Rerank        port.RerankRuntimeConfig
	MinerU        port.MinerURuntimeConfig
}

type Service struct {
	repo      port.PlatformConfigRepo
	cipher    port.SecretCipher
	fallbacks Fallbacks
}

func NewService(repo port.PlatformConfigRepo, cipher port.SecretCipher, fallbacks Fallbacks) (*Service, error) {
	if repo == nil || cipher == nil {
		return nil, fmt.Errorf("platform config: repo and secret cipher are required")
	}
	return &Service{repo: repo, cipher: cipher, fallbacks: fallbacks}, nil
}

type ConfigItemView struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Source           string `json:"source"`
	Active           bool   `json:"active"`
	ReadOnly         bool   `json:"read_only"`
	Configured       bool   `json:"configured"`
	SecretConfigured bool   `json:"secret_configured"`
	Config           any    `json:"config"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type ConfigGroupView struct {
	Kind     string           `json:"kind"`
	ActiveID string           `json:"active_id"`
	Items    []ConfigItemView `json:"items"`
}

type ConfigSummaryView struct {
	LLM       bool `json:"llm"`
	Embedding bool `json:"embedding"`
	Rerank    bool `json:"rerank"`
	MinerU    bool `json:"mineru"`
}

type CatalogView struct {
	WorkspaceName string            `json:"workspace_name"`
	Summary       ConfigSummaryView `json:"summary"`
	Groups        []ConfigGroupView `json:"groups"`
}

type UpsertInput struct {
	Name     string          `json:"name"`
	Config   json.RawMessage `json:"config"`
	Secret   string          `json:"secret,omitempty"`
	Activate bool            `json:"activate,omitempty"`
}

func (s *Service) Catalog(ctx context.Context, workspaceID string) (*CatalogView, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	groups := make([]ConfigGroupView, 0, len(kinds))
	for _, kind := range kinds {
		rows, err := s.repo.ListPlatformConfigs(ctx, workspaceID, kind)
		if err != nil {
			return nil, err
		}
		group, err := s.groupView(kind, rows)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	view := &CatalogView{WorkspaceName: s.fallbacks.WorkspaceName, Groups: groups}
	for _, group := range groups {
		for _, item := range group.Items {
			if !item.Active {
				continue
			}
			switch group.Kind {
			case model.PlatformConfigLLM:
				view.Summary.LLM = item.Configured
			case model.PlatformConfigEmbedding:
				view.Summary.Embedding = item.Configured
			case model.PlatformConfigRerank:
				view.Summary.Rerank = item.Configured
			case model.PlatformConfigMinerU:
				view.Summary.MinerU = item.Configured
			}
			break
		}
	}
	return view, nil
}

func (s *Service) Create(ctx context.Context, workspaceID, kind string, input UpsertInput) (*ConfigItemView, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	kind, err := normalizeKind(kind)
	if err != nil {
		return nil, err
	}
	name, settings, err := normalizeInput(kind, input.Name, input.Config)
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(input.Secret)
	if secret == "" {
		return nil, fmt.Errorf("密钥不能为空")
	}
	ciphertext, err := s.cipher.Encrypt(secret)
	if err != nil {
		return nil, fmt.Errorf("加密平台配置密钥: %w", err)
	}
	value := &model.PlatformConfig{WorkspaceID: workspaceID, Kind: kind, Name: name,
		Settings: settings, SecretCiphertext: ciphertext, Active: input.Activate}
	if err := s.repo.CreatePlatformConfig(ctx, value); err != nil {
		return nil, fmt.Errorf("创建平台配置: %w", err)
	}
	view, err := s.databaseItemView(*value)
	return &view, err
}

func (s *Service) Update(ctx context.Context, workspaceID, kind, id string, input UpsertInput) (*ConfigItemView, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	kind, err := normalizeKind(kind)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == fileConfigID {
		return nil, fmt.Errorf("配置文件兜底项不可编辑")
	}
	value, err := s.repo.GetPlatformConfig(ctx, workspaceID, kind, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	name, settings, err := normalizeInput(kind, input.Name, input.Config)
	if err != nil {
		return nil, err
	}
	value.Name, value.Settings = name, settings
	if secret := strings.TrimSpace(input.Secret); secret != "" {
		value.SecretCiphertext, err = s.cipher.Encrypt(secret)
		if err != nil {
			return nil, fmt.Errorf("加密平台配置密钥: %w", err)
		}
	}
	if err := s.repo.UpdatePlatformConfig(ctx, value); err != nil {
		return nil, fmt.Errorf("更新平台配置: %w", err)
	}
	view, err := s.databaseItemView(*value)
	return &view, err
}

func (s *Service) Delete(ctx context.Context, workspaceID, kind, id string) error {
	workspaceID = normalizeWorkspaceID(workspaceID)
	kind, err := normalizeKind(kind)
	if err != nil {
		return err
	}
	if strings.TrimSpace(id) == fileConfigID {
		return fmt.Errorf("配置文件兜底项不可删除")
	}
	return s.repo.DeletePlatformConfig(ctx, workspaceID, kind, strings.TrimSpace(id))
}

func (s *Service) Activate(ctx context.Context, workspaceID, kind, id string) error {
	workspaceID = normalizeWorkspaceID(workspaceID)
	kind, err := normalizeKind(kind)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == fileConfigID {
		return s.repo.DeactivatePlatformConfigs(ctx, workspaceID, kind)
	}
	value, err := s.repo.GetPlatformConfig(ctx, workspaceID, kind, id)
	if err != nil {
		return err
	}
	if _, err := s.decodeDatabaseConfig(*value); err != nil {
		return fmt.Errorf("配置不可激活: %w", err)
	}
	return s.repo.ActivatePlatformConfig(ctx, workspaceID, kind, id)
}

func (s *Service) ResolveLLM(ctx context.Context) (port.LLMRuntimeConfig, error) {
	value, err := s.active(ctx, model.PlatformConfigLLM)
	if errors.Is(err, port.ErrPlatformConfigNotFound) {
		return s.fallbacks.LLM, nil
	}
	if err != nil {
		return port.LLMRuntimeConfig{}, err
	}
	decoded, err := s.decodeDatabaseConfig(*value)
	if err != nil {
		return port.LLMRuntimeConfig{}, err
	}
	return decoded.(port.LLMRuntimeConfig), nil
}

func (s *Service) ResolveEmbedding(ctx context.Context) (port.EmbeddingRuntimeConfig, error) {
	value, err := s.active(ctx, model.PlatformConfigEmbedding)
	if errors.Is(err, port.ErrPlatformConfigNotFound) {
		return s.fallbacks.Embedding, nil
	}
	if err != nil {
		return port.EmbeddingRuntimeConfig{}, err
	}
	decoded, err := s.decodeDatabaseConfig(*value)
	if err != nil {
		return port.EmbeddingRuntimeConfig{}, err
	}
	return decoded.(port.EmbeddingRuntimeConfig), nil
}

func (s *Service) ResolveRerank(ctx context.Context) (port.RerankRuntimeConfig, error) {
	value, err := s.active(ctx, model.PlatformConfigRerank)
	if errors.Is(err, port.ErrPlatformConfigNotFound) {
		return s.fallbacks.Rerank, nil
	}
	if err != nil {
		return port.RerankRuntimeConfig{}, err
	}
	decoded, err := s.decodeDatabaseConfig(*value)
	if err != nil {
		return port.RerankRuntimeConfig{}, err
	}
	return decoded.(port.RerankRuntimeConfig), nil
}

func (s *Service) ResolveMinerU(ctx context.Context) (port.MinerURuntimeConfig, error) {
	value, err := s.active(ctx, model.PlatformConfigMinerU)
	if errors.Is(err, port.ErrPlatformConfigNotFound) {
		return s.fallbacks.MinerU, nil
	}
	if err != nil {
		return port.MinerURuntimeConfig{}, err
	}
	decoded, err := s.decodeDatabaseConfig(*value)
	if err != nil {
		return port.MinerURuntimeConfig{}, err
	}
	return decoded.(port.MinerURuntimeConfig), nil
}

func (s *Service) active(ctx context.Context, kind string) (*model.PlatformConfig, error) {
	return s.repo.GetActivePlatformConfig(ctx, defaultWorkspaceID, kind)
}

func (s *Service) groupView(kind string, rows []model.PlatformConfig) (ConfigGroupView, error) {
	activeID := fileConfigID
	for _, row := range rows {
		if row.Active {
			activeID = row.ID
			break
		}
	}
	file := s.fileItemView(kind, activeID == fileConfigID)
	items := make([]ConfigItemView, 0, len(rows)+1)
	items = append(items, file)
	for _, row := range rows {
		item, err := s.databaseItemView(row)
		if err != nil {
			return ConfigGroupView{}, err
		}
		items = append(items, item)
	}
	return ConfigGroupView{Kind: kind, ActiveID: activeID, Items: items}, nil
}

func (s *Service) fileItemView(kind string, active bool) ConfigItemView {
	item := ConfigItemView{ID: fileConfigID, Kind: kind, Name: "config.yaml（兜底）",
		Source: "file", Active: active, ReadOnly: true}
	switch kind {
	case model.PlatformConfigLLM:
		item.Config, item.SecretConfigured = publicLLM(s.fallbacks.LLM), s.fallbacks.LLM.APIKey != ""
		item.Configured = llmConfigured(s.fallbacks.LLM)
	case model.PlatformConfigEmbedding:
		item.Config, item.SecretConfigured = publicEmbedding(s.fallbacks.Embedding), s.fallbacks.Embedding.APIKey != ""
		item.Configured = embeddingConfigured(s.fallbacks.Embedding)
	case model.PlatformConfigRerank:
		item.Config, item.SecretConfigured = publicRerank(s.fallbacks.Rerank), s.fallbacks.Rerank.APIKey != ""
		item.Configured = rerankConfigured(s.fallbacks.Rerank)
	case model.PlatformConfigMinerU:
		item.Config, item.SecretConfigured = publicMinerU(s.fallbacks.MinerU), s.fallbacks.MinerU.APIToken != ""
		item.Configured = minerUConfigured(s.fallbacks.MinerU)
	}
	return item
}

func (s *Service) databaseItemView(value model.PlatformConfig) (ConfigItemView, error) {
	config, err := decodePublicConfig(value.Kind, value.Settings)
	if err != nil {
		return ConfigItemView{}, fmt.Errorf("平台配置 %s 内容损坏: %w", value.ID, err)
	}
	secret, err := s.cipher.Decrypt(value.SecretCiphertext)
	if err != nil {
		return ConfigItemView{}, fmt.Errorf("平台配置 %s 密钥无法解密: %w", value.ID, err)
	}
	secretConfigured := strings.TrimSpace(secret) != ""
	return ConfigItemView{ID: value.ID, Kind: value.Kind, Name: value.Name, Source: "database",
		Active: value.Active, ReadOnly: false, Configured: secretConfigured,
		SecretConfigured: secretConfigured, Config: config,
		CreatedAt: value.CreatedAt.Format(time.RFC3339), UpdatedAt: value.UpdatedAt.Format(time.RFC3339)}, nil
}

func (s *Service) decodeDatabaseConfig(value model.PlatformConfig) (any, error) {
	secret, err := s.cipher.Decrypt(value.SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("解密密钥失败: %w", err)
	}
	decoded, err := decodePublicConfig(value.Kind, value.Settings)
	if err != nil {
		return nil, err
	}
	switch config := decoded.(type) {
	case port.LLMRuntimeConfig:
		config.APIKey = secret
		return config, nil
	case port.EmbeddingRuntimeConfig:
		config.APIKey = secret
		return config, nil
	case port.RerankRuntimeConfig:
		config.APIKey = secret
		return config, nil
	case port.MinerURuntimeConfig:
		config.APIToken, config.Enabled = secret, true
		return config, nil
	default:
		return nil, fmt.Errorf("未知配置类型 %s", value.Kind)
	}
}

func normalizeInput(kind, name string, raw json.RawMessage) (string, json.RawMessage, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) == 0 || len([]rune(name)) > 80 {
		return "", nil, fmt.Errorf("配置名称必须为 1..80 个字符")
	}
	decoded, err := decodePublicConfig(kind, raw)
	if err != nil {
		return "", nil, err
	}
	if err := validateConfig(kind, decoded); err != nil {
		return "", nil, err
	}
	normalized, err := json.Marshal(decoded)
	return name, normalized, err
}

func decodePublicConfig(kind string, raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("config 不能为空")
	}
	var target any
	switch kind {
	case model.PlatformConfigLLM:
		target = &port.LLMRuntimeConfig{}
	case model.PlatformConfigEmbedding:
		target = &port.EmbeddingRuntimeConfig{}
	case model.PlatformConfigRerank:
		target = &port.RerankRuntimeConfig{}
	case model.PlatformConfigMinerU:
		target = &port.MinerURuntimeConfig{}
	default:
		return nil, fmt.Errorf("未知平台配置类型 %q", kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("config 格式错误: %w", err)
	}
	switch value := target.(type) {
	case *port.LLMRuntimeConfig:
		if value.Provider == "" {
			value.Provider = "openai"
		}
		if value.TimeoutMs <= 0 {
			value.TimeoutMs = 300000
		}
		return *value, nil
	case *port.EmbeddingRuntimeConfig:
		if value.Dimensions == 0 {
			value.Dimensions = 1024
		}
		if value.BatchSize <= 0 {
			value.BatchSize = 32
		}
		if value.TimeoutMs <= 0 {
			value.TimeoutMs = 30000
		}
		return *value, nil
	case *port.RerankRuntimeConfig:
		if value.TimeoutMs <= 0 {
			value.TimeoutMs = 60000
		}
		return *value, nil
	case *port.MinerURuntimeConfig:
		value.Enabled = true
		if value.ModelVersion == "" {
			value.ModelVersion = "vlm"
		}
		if value.TimeoutMs <= 0 {
			value.TimeoutMs = 600000
		}
		if value.PollIntervalMs <= 0 {
			value.PollIntervalMs = 5000
		}
		return *value, nil
	}
	return nil, fmt.Errorf("未知平台配置类型 %q", kind)
}

func validateConfig(kind string, value any) error {
	switch config := value.(type) {
	case port.LLMRuntimeConfig:
		if config.Provider != "openai" && config.Provider != "anthropic" {
			return fmt.Errorf("LLM provider 必须为 openai 或 anthropic")
		}
		if err := validateHTTPURL(config.BaseURL, "LLM base_url"); err != nil {
			return err
		}
		if strings.TrimSpace(config.Model) == "" {
			return fmt.Errorf("LLM model 不能为空")
		}
		if config.Temperature < 0 || config.Temperature > 2 {
			return fmt.Errorf("LLM temperature 必须在 0..2 之间")
		}
		if config.MaxTokens < 0 || config.TimeoutMs <= 0 {
			return fmt.Errorf("LLM max_tokens 不能为负数且 timeout_ms 必须大于 0")
		}
	case port.EmbeddingRuntimeConfig:
		if err := validateHTTPURL(config.BaseURL, "Embedding base_url"); err != nil {
			return err
		}
		if strings.TrimSpace(config.Model) == "" {
			return fmt.Errorf("Embedding model 不能为空")
		}
		if config.Dimensions != 1024 {
			return fmt.Errorf("Embedding dimensions 必须为 1024（与当前 pgvector 列一致）")
		}
		if config.BatchSize < 1 || config.BatchSize > 2048 || config.TimeoutMs <= 0 {
			return fmt.Errorf("Embedding batch_size 必须为 1..2048 且 timeout_ms 必须大于 0")
		}
	case port.RerankRuntimeConfig:
		if err := validateHTTPURL(config.BaseURL, "Rerank base_url"); err != nil {
			return err
		}
		if strings.TrimSpace(config.Model) == "" || config.TimeoutMs <= 0 {
			return fmt.Errorf("Rerank model 不能为空且 timeout_ms 必须大于 0")
		}
	case port.MinerURuntimeConfig:
		if err := validateHTTPURL(config.APIURL, "MinerU api_url"); err != nil {
			return err
		}
		if config.ModelVersion != "vlm" && config.ModelVersion != "pipeline" {
			return fmt.Errorf("MinerU model_version 必须为 vlm 或 pipeline")
		}
		if config.TimeoutMs <= 0 || config.PollIntervalMs <= 0 {
			return fmt.Errorf("MinerU timeout_ms 与 poll_interval_ms 必须大于 0")
		}
	default:
		return fmt.Errorf("未知平台配置类型 %s", kind)
	}
	return nil
}

func validateHTTPURL(raw, field string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s 必须是 http/https URL", field)
	}
	return nil
}

func normalizeWorkspaceID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return defaultWorkspaceID
}

func normalizeKind(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, kind := range kinds {
		if value == kind {
			return value, nil
		}
	}
	return "", fmt.Errorf("未知平台配置类型 %q", value)
}

func publicLLM(value port.LLMRuntimeConfig) port.LLMRuntimeConfig { value.APIKey = ""; return value }
func publicEmbedding(value port.EmbeddingRuntimeConfig) port.EmbeddingRuntimeConfig {
	value.APIKey = ""
	return value
}
func publicRerank(value port.RerankRuntimeConfig) port.RerankRuntimeConfig {
	value.APIKey = ""
	return value
}
func publicMinerU(value port.MinerURuntimeConfig) port.MinerURuntimeConfig {
	value.APIToken = ""
	return value
}

func llmConfigured(value port.LLMRuntimeConfig) bool {
	return strings.TrimSpace(value.APIKey) != "" && strings.TrimSpace(value.BaseURL) != "" && strings.TrimSpace(value.Model) != ""
}
func embeddingConfigured(value port.EmbeddingRuntimeConfig) bool {
	return strings.TrimSpace(value.APIKey) != "" && strings.TrimSpace(value.BaseURL) != "" && strings.TrimSpace(value.Model) != ""
}
func rerankConfigured(value port.RerankRuntimeConfig) bool {
	return strings.TrimSpace(value.APIKey) != "" && strings.TrimSpace(value.BaseURL) != "" && strings.TrimSpace(value.Model) != ""
}
func minerUConfigured(value port.MinerURuntimeConfig) bool {
	return value.Enabled && strings.TrimSpace(value.APIToken) != "" && strings.TrimSpace(value.APIURL) != ""
}
