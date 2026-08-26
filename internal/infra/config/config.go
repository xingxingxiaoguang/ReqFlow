// Package config 提供 ReqFlow 的配置加载与校验。
// 全部配置来自本地 YAML 文件（env 可覆盖），代码与打包产物零硬编码配置；
// 首次启动若无配置文件，会从内嵌模板生成 ./config.yaml 并提示填写。
package config

import (
	_ "embed"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed example.yaml
var exampleYAML []byte

// Example 返回内嵌的示例配置内容（用于首启生成与文档展示）。
func Example() string { return string(exampleYAML) }

type Config struct {
	Server struct {
		Port      int    `yaml:"port"           env:"REQFLOW_SERVER_PORT"`
		LogLevel  string `yaml:"log_level"      env:"REQFLOW_SERVER_LOG_LEVEL"`
		LogFormat string `yaml:"log_format"     env:"REQFLOW_SERVER_LOG_FORMAT"`
	} `yaml:"server"`

	Database struct {
		DSN             string `yaml:"dsn"               env:"REQFLOW_DATABASE_DSN"`
		AutoMigrate     bool   `yaml:"auto_migrate"      env:"REQFLOW_DATABASE_AUTO_MIGRATE"`
		RetryCount      int    `yaml:"retry_count"       env:"REQFLOW_DATABASE_RETRY_COUNT"`
		RetryIntervalMs int    `yaml:"retry_interval_ms" env:"REQFLOW_DATABASE_RETRY_INTERVAL_MS"`
	} `yaml:"database"`

	LLM struct {
		Provider    string  `yaml:"provider"    env:"REQFLOW_LLM_PROVIDER"`
		BaseURL     string  `yaml:"base_url"    env:"REQFLOW_LLM_BASE_URL"`
		APIKey      string  `yaml:"api_key"     env:"REQFLOW_LLM_API_KEY"`
		Model       string  `yaml:"model"       env:"REQFLOW_LLM_MODEL"`
		Temperature float64 `yaml:"temperature" env:"REQFLOW_LLM_TEMPERATURE"`
		MaxTokens   int     `yaml:"max_tokens"  env:"REQFLOW_LLM_MAX_TOKENS"`
		TimeoutMs   int     `yaml:"timeout_ms"  env:"REQFLOW_LLM_TIMEOUT_MS"`
		AgentMode   bool    `yaml:"agent_mode"  env:"REQFLOW_LLM_AGENT_MODE"`
	} `yaml:"llm"`

	Embedding struct {
		BaseURL   string `yaml:"base_url"   env:"REQFLOW_EMBEDDING_BASE_URL"`
		APIKey    string `yaml:"api_key"    env:"REQFLOW_EMBEDDING_API_KEY"`
		Model     string `yaml:"model"      env:"REQFLOW_EMBEDDING_MODEL"`
		Dimensions int   `yaml:"dimensions" env:"REQFLOW_EMBEDDING_DIMENSIONS"`
		BatchSize int    `yaml:"batch_size" env:"REQFLOW_EMBEDDING_BATCH_SIZE"`
		TimeoutMs int    `yaml:"timeout_ms" env:"REQFLOW_EMBEDDING_TIMEOUT_MS"`
	} `yaml:"embedding"`

	Match struct {
		DuplicateThreshold float64 `yaml:"duplicate_threshold" env:"REQFLOW_MATCH_DUPLICATE_THRESHOLD"`
	} `yaml:"match"`

	Parser struct {
		MaxFileMB int `yaml:"max_file_mb" env:"REQFLOW_PARSER_MAX_FILE_MB"`
		MinerU    struct {
			Enabled       bool   `yaml:"enabled"        env:"REQFLOW_PARSER_MINERU_ENABLED"`
			APIURL        string `yaml:"api_url"        env:"REQFLOW_PARSER_MINERU_API_URL"`
			APIToken      string `yaml:"api_token"      env:"REQFLOW_PARSER_MINERU_API_TOKEN"`
			ModelVersion  string `yaml:"model_version"  env:"REQFLOW_PARSER_MINERU_MODEL_VERSION"`
			TimeoutMs     int    `yaml:"timeout_ms"     env:"REQFLOW_PARSER_MINERU_TIMEOUT_MS"`
			PollIntervalMs int  `yaml:"poll_interval_ms" env:"REQFLOW_PARSER_MINERU_POLL_INTERVAL_MS"`
		} `yaml:"mineru"`
	} `yaml:"parser"`

	Security struct {
		EncryptionKey string `yaml:"encryption_key" env:"REQFLOW_SECURITY_ENCRYPTION_KEY"`
	} `yaml:"security"`

	Workspace struct {
		Name      string `yaml:"name"       env:"REQFLOW_WORKSPACE_NAME"`
		UploadDir string `yaml:"upload_dir" env:"REQFLOW_WORKSPACE_UPLOAD_DIR"`
		DemandDir string `yaml:"demand_dir" env:"REQFLOW_WORKSPACE_DEMAND_DIR"`
	} `yaml:"workspace"`
}

// Load 读取配置文件并应用环境变量覆盖。
// path 为空时依次尝试 $REQFLOW_CONFIG、./config.yaml；文件不存在时返回 ErrNoConfig。
func Load(path string) (*Config, error) {
	if path == "" {
		path = os.Getenv("REQFLOW_CONFIG")
	}
	if path == "" {
		path = "config.yaml"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoConfig, path)
		}
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败（%s）: %w", path, err)
	}
	applyEnvOverrides(cfg)
	applyDefaults(cfg)
	return cfg, nil
}

// ErrNoConfig 表示配置文件不存在（首启由 main 负责生成模板）。
var ErrNoConfig = fmt.Errorf("配置文件不存在")

// applyEnvOverrides 遍历 env 标签，环境变量存在则覆盖对应字段。
func applyEnvOverrides(cfg *Config) {
	override(reflect.ValueOf(cfg).Elem())
}

func override(v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		tag := t.Field(i).Tag.Get("env")
		if tag != "" {
			if val, ok := os.LookupEnv(tag); ok {
				setField(f, val)
			}
			continue
		}
		if f.Kind() == reflect.Struct {
			override(f)
		}
	}
}

func setField(f reflect.Value, val string) {
	switch f.Kind() {
	case reflect.String:
		f.SetString(val)
	case reflect.Int:
		if n, err := strconv.Atoi(val); err == nil {
			f.SetInt(int64(n))
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(val); err == nil {
			f.SetBool(b)
		}
	case reflect.Float64:
		if fl, err := strconv.ParseFloat(val, 64); err == nil {
			f.SetFloat(fl)
		}
	}
}

// applyDefaults 只补齐影响运行的边角默认值；业务配置一律以配置文件为准。
func applyDefaults(cfg *Config) {
	if cfg.Server.LogLevel == "" {
		cfg.Server.LogLevel = "info"
	}
	if cfg.Server.LogFormat == "" {
		cfg.Server.LogFormat = "text"
	}
	if cfg.Workspace.UploadDir == "" {
		cfg.Workspace.UploadDir = "./data/uploads"
	}
	if cfg.Workspace.DemandDir == "" {
		cfg.Workspace.DemandDir = "./data/demands"
	}
}

// Validate 校验配置完整性。
// errs 为致命问题（阻断启动）；warns 为功能降级提示（对应功能不可用但服务可启动）。
func (c *Config) Validate() (errs, warns []string) {
	if strings.TrimSpace(c.Database.DSN) == "" {
		errs = append(errs, "database.dsn 未配置（数据库为核心依赖，必须填写）")
	}
	if c.Embedding.APIKey != "" && c.Embedding.Dimensions != 1024 {
		errs = append(errs, fmt.Sprintf(
			"embedding.dimensions = %d，但当前向量列固定 1024 维；请改用 1024 维模型（如 BAAI/bge-m3）或调整迁移后重建库", c.Embedding.Dimensions))
	}
	if c.LLM.Provider != "" && c.LLM.Provider != "openai" && c.LLM.Provider != "anthropic" {
		errs = append(errs, fmt.Sprintf("llm.provider = %q 非法，必须为 openai 或 anthropic", c.LLM.Provider))
	}
	if c.LLM.APIKey == "" {
		warns = append(warns, "llm.api_key 未配置：需求文档 LLM 分析不可用（其余功能不受影响）")
	}
	if c.Embedding.APIKey == "" {
		warns = append(warns, "embedding.api_key 未配置：语义匹配降级为仅精确匹配，生成数据集时不写入向量")
	}
	if c.Parser.MinerU.Enabled && c.Parser.MinerU.APIToken == "" {
		warns = append(warns, "parser.mineru.api_token 未配置：PDF 解析不可用（docx/md/txt 不受影响）")
	}
	return errs, warns
}

// LLMReady / EmbeddingReady / MinerUReady 供运行时做功能可用性判断。
func (c *Config) LLMReady() bool       { return c.LLM.APIKey != "" && c.LLM.BaseURL != "" && c.LLM.Model != "" }
func (c *Config) EmbeddingReady() bool { return c.Embedding.APIKey != "" && c.Embedding.BaseURL != "" && c.Embedding.Model != "" }
func (c *Config) MinerUReady() bool    { return c.Parser.MinerU.Enabled && c.Parser.MinerU.APIToken != "" }

// FilledSecrets 列出已配置非空的敏感字段名（仅名称不含值，用于启动日志自检）。
func (c *Config) FilledSecrets() []string {
	var out []string
	if c.LLM.APIKey != "" {
		out = append(out, "llm.api_key")
	}
	if c.Embedding.APIKey != "" {
		out = append(out, "embedding.api_key")
	}
	if c.Parser.MinerU.APIToken != "" {
		out = append(out, "parser.mineru.api_token")
	}
	if c.Security.EncryptionKey != "" {
		out = append(out, "security.encryption_key")
	}
	return out
}

// CheckExampleLeak 检查示例模板文件是否被填入真实密钥（该文件设计为随代码入库分享）。
// 返回被填写的敏感字段名列表；文件不存在、解析失败或全部为空时返回 nil。
func CheckExampleLeak(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var probe struct {
		LLM       struct{ APIKey string `yaml:"api_key"` } `yaml:"llm"`
		Embedding struct{ APIKey string `yaml:"api_key"` } `yaml:"embedding"`
		Parser    struct {
			MinerU struct{ APIToken string `yaml:"api_token"` } `yaml:"mineru"`
		} `yaml:"parser"`
		Security struct{ EncryptionKey string `yaml:"encryption_key"` } `yaml:"security"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	var out []string
	if probe.LLM.APIKey != "" {
		out = append(out, "llm.api_key")
	}
	if probe.Embedding.APIKey != "" {
		out = append(out, "embedding.api_key")
	}
	if probe.Parser.MinerU.APIToken != "" {
		out = append(out, "parser.mineru.api_token")
	}
	if probe.Security.EncryptionKey != "" {
		out = append(out, "security.encryption_key")
	}
	return out
}
