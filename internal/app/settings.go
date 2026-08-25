package app

import (
	"context"

	"reqflow/internal/port"
)

// SettingsView 设置页的只读脱敏视图（由 cmd 从配置构造后注入，app 不感知配置文件）。
type SettingsView struct {
	WorkspaceName string `json:"workspaceName"`
	LLM           struct {
		BaseURL     string `json:"baseUrl"`
		Model       string `json:"model"`
		Configured  bool   `json:"configured"`
	} `json:"llm"`
	Embedding struct {
		BaseURL    string `json:"baseUrl"`
		Model      string `json:"model"`
		Configured bool   `json:"configured"`
	} `json:"embedding"`
	PingCode struct {
		Host       string `json:"host"`
		Configured bool   `json:"configured"`
	} `json:"pingcode"`
	MinerU struct {
		Enabled    bool `json:"enabled"`
		Configured bool `json:"configured"`
	} `json:"mineru"`
}

// SettingsService 设置用例：查看脱敏配置 + 各外部依赖连通性测试。
// 配置只读：修改需编辑本地 config.yaml 后重启（零硬编码原则）。
type SettingsService struct {
	view     SettingsView
	llm      port.LLMClient
	platform port.PlatformClient
}

// NewSettingsService 构造用例。
func NewSettingsService(view SettingsView, llm port.LLMClient, platform port.PlatformClient) *SettingsService {
	return &SettingsService{view: view, llm: llm, platform: platform}
}

// View 返回脱敏配置视图。
func (s *SettingsService) View() SettingsView { return s.view }

// TestLLM 测试 LLM 连通性。
func (s *SettingsService) TestLLM(ctx context.Context) error { return s.llm.Ping(ctx) }

// TestPingCode 测试协作平台连通性。
func (s *SettingsService) TestPingCode(ctx context.Context) error { return s.platform.TestConnection(ctx) }
