// ReqFlow 入口：全项目唯一知道所有具体实现的组装点。
// 依赖方向在此收口：infra 实现 → 注入 app 用例 → 挂到 httpgin。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"reqflow/internal/app"
	"reqflow/internal/infra/config"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/embedding"
	"reqflow/internal/infra/httpgin"
	"reqflow/internal/infra/llm"
	"reqflow/internal/infra/parser"
	"reqflow/internal/infra/pingcode"
	"reqflow/internal/infra/repository"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（默认 $REQFLOW_CONFIG 或 ./config.yaml）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if errors.Is(err, config.ErrNoConfig) {
		if werr := os.WriteFile("config.yaml", []byte(config.Example()), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "未找到配置文件且生成模板失败: %v\n", werr)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "未找到配置文件，已生成 ./config.yaml 模板。请填写后重新启动。")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(cfg.Server.LogLevel)}))
	slog.SetDefault(logger)

	if errs, warns := cfg.Validate(); len(errs) > 0 {
		for _, e := range errs {
			logger.Error("配置错误", "detail", e)
		}
		for _, w := range warns {
			logger.Warn("配置提醒", "detail", w)
		}
		os.Exit(1)
	} else {
		for _, w := range warns {
			logger.Warn("配置提醒", "detail", w)
		}
	}

	/* ---- 数据库（Docker PG + pgvector，自动迁移） ---- */
	db, err := database.Connect(cfg.Database.DSN, cfg.Database.RetryCount, cfg.Database.RetryIntervalMs)
	if err != nil {
		logger.Error("数据库连接失败（请先 docker compose up -d 启动 PG）", "err", err)
		os.Exit(1)
	}
	if cfg.Database.AutoMigrate {
		if err := database.Migrate(db); err != nil {
			logger.Error("数据库迁移失败", "err", err)
			os.Exit(1)
		}
	}

	/* ---- infra 实现 ---- */
	pcClient := pingcode.New(pingcode.Options{
		Host:         cfg.PingCode.Host,
		ClientID:     cfg.PingCode.ClientID,
		ClientSecret: cfg.PingCode.ClientSecret,
	})
	llmClient := llm.New(llm.Options{
		BaseURL:     cfg.LLM.BaseURL,
		APIKey:      cfg.LLM.APIKey,
		Model:       cfg.LLM.Model,
		Temperature: cfg.LLM.Temperature,
		MaxTokens:   cfg.LLM.MaxTokens,
		Timeout:     time.Duration(cfg.LLM.TimeoutMs) * time.Millisecond,
	})
	embedClient := embedding.New(embedding.Options{
		BaseURL:   cfg.Embedding.BaseURL,
		APIKey:    cfg.Embedding.APIKey,
		Model:     cfg.Embedding.Model,
		BatchSize: cfg.Embedding.BatchSize,
		Timeout:   time.Duration(cfg.Embedding.TimeoutMs) * time.Millisecond,
	})
	docParser := parser.New(parser.Options{
		MaxFileMB: cfg.Parser.MaxFileMB,
		MinerU: parser.MinerUOptions{
			Enabled:      cfg.Parser.MinerU.Enabled,
			APIURL:       cfg.Parser.MinerU.APIURL,
			APIToken:     cfg.Parser.MinerU.APIToken,
			ModelVersion: cfg.Parser.MinerU.ModelVersion,
			Timeout:      time.Duration(cfg.Parser.MinerU.TimeoutMs) * time.Millisecond,
			PollInterval: time.Duration(cfg.Parser.MinerU.PollIntervalMs) * time.Millisecond,
		},
	})

	projectRepo := repository.NewProjectRepo(db)
	workItemRepo := repository.NewWorkItemRepo(db)
	metaRepo := repository.NewMetaRepo(db)
	importRepo := repository.NewImportRepo(db)

	/* ---- app 用例 ---- */
	syncSvc := app.NewSyncService(pcClient, embedClient, projectRepo, workItemRepo, metaRepo,
		3, cfg.PingCode.SyncWorkItemBatchSize, time.Duration(cfg.PingCode.SyncBatchDelayMs)*time.Millisecond)
	parseSvc := app.NewParseService(docParser)
	analyzeSvc := app.NewAnalyzeService(llmClient, importRepo, cfg.Workspace.DemandDir)
	matchSvc := app.NewMatchService(projectRepo, workItemRepo, embedClient,
		cfg.Match.ProjectTopN, cfg.Match.DuplicateThreshold)
	importSvc := app.NewImportService(pcClient, metaRepo, importRepo, projectRepo,
		cfg.PingCode.WorkloadUnit, cfg.PingCode.ImportConcurrency)
	recordSvc := app.NewRecordService(importRepo)
	browseSvc := app.NewBrowseService(projectRepo, workItemRepo)
	overviewSvc := app.NewOverviewService(projectRepo, workItemRepo, importRepo)

	var settingsView app.SettingsView
	settingsView.WorkspaceName = cfg.Workspace.Name
	settingsView.LLM.BaseURL, settingsView.LLM.Model, settingsView.LLM.Configured = cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLMReady()
	settingsView.Embedding.BaseURL, settingsView.Embedding.Model, settingsView.Embedding.Configured = cfg.Embedding.BaseURL, cfg.Embedding.Model, cfg.EmbeddingReady()
	settingsView.PingCode.Host, settingsView.PingCode.Configured = cfg.PingCode.Host, cfg.PingCodeReady()
	settingsView.MinerU.Enabled, settingsView.MinerU.Configured = cfg.Parser.MinerU.Enabled, cfg.MinerUReady()
	settingsSvc := app.NewSettingsService(settingsView, llmClient, pcClient)

	/* ---- HTTP ---- */
	engine := httpgin.New(httpgin.Services{
		Sync: syncSvc, Parse: parseSvc, Analyze: analyzeSvc, Match: matchSvc,
		Import: importSvc, Record: recordSvc, Settings: settingsSvc,
		Overview: overviewSvc, Browse: browseSvc,
		UploadDir: cfg.Workspace.UploadDir,
		MaxFileMB: int64(cfg.Parser.MaxFileMB),
	})
	mountStatic(engine)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: engine,
	}
	go func() {
		logger.Info("ReqFlow 启动", "port", cfg.Server.Port, "workspace", cfg.Workspace.Name)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP 服务异常退出", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("正在优雅关闭…")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func parseLevel(s string) (lv slog.Level) {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
