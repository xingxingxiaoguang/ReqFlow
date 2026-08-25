// Package log 提供统一的结构化日志构造。
// 实例由 cmd 组装后注入各依赖，业务与 infra 均不自行读取配置。
package log

import (
	"log/slog"
	"os"
	"strings"
)

// New 按级别与格式构造 *slog.Logger（level: debug|info|warn|error；format: text|json）。
func New(level, format string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}

	var h slog.Handler
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
