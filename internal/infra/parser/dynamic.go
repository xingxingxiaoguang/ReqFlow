package parser

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// DynamicParser 仅在 PDF 解析时读取当前 MinerU 配置；本地格式不依赖外部配置。
type DynamicParser struct {
	maxFileMB int
	resolver  port.PlatformConfigResolver
}

func NewDynamic(maxFileMB int, resolver port.PlatformConfigResolver) *DynamicParser {
	return &DynamicParser{maxFileMB: maxFileMB, resolver: resolver}
}

func (*DynamicParser) ParserName() string    { return Name }
func (*DynamicParser) ParserVersion() string { return Version }

func (p *DynamicParser) Parse(ctx context.Context, source port.ParseSource,
	onProgress func(port.ParseProgress)) ([]model.DocumentBlock, error) {
	options := Options{MaxFileMB: p.maxFileMB}
	if strings.EqualFold(filepath.Ext(source.Filename), ".pdf") {
		if p.resolver == nil {
			return nil, fmt.Errorf("MinerU 平台配置解析器未初始化")
		}
		config, err := p.resolver.ResolveMinerU(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取当前 MinerU 配置: %w", err)
		}
		options.MinerU = MinerUOptions{Enabled: config.Enabled, APIURL: config.APIURL,
			APIToken: config.APIToken, ModelVersion: config.ModelVersion,
			Timeout:      time.Duration(config.TimeoutMs) * time.Millisecond,
			PollInterval: time.Duration(config.PollIntervalMs) * time.Millisecond}
	}
	return New(options).Parse(ctx, source, onProgress)
}
