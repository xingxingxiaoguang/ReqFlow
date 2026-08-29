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
	"strings"
	"syscall"
	"time"

	"log/slog"

	"reqflow/internal/app"
	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/infra/blobstore"
	"reqflow/internal/infra/config"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/embedding"
	"reqflow/internal/infra/httpgin"
	"reqflow/internal/infra/llm"
	"reqflow/internal/infra/parser"
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

	// 密钥安全自检：已加载的密钥只打名单不打印值；example 模板被填入真实密钥时醒目告警
	if filled := cfg.FilledSecrets(); len(filled) > 0 {
		logger.Info("已加载密钥（仅显示名称）", "keys", strings.Join(filled, ", "))
	}
	if leaked := checkExampleTemplate(); len(leaked) > 0 {
		logger.Error("⚠️ 检测到 config.example.yaml 中被填入了真实密钥（该文件会随代码分享！）",
			"fields", strings.Join(leaked, ", "),
			"处理", "请立即将值清空并轮换已暴露的密钥；真实密钥只放 config.yaml 或环境变量")
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
	llmClient := llm.New(llm.Options{
		Provider:    cfg.LLM.Provider,
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
	localBlobs, err := blobstore.NewLocal(cfg.Workspace.BlobDir)
	if err != nil {
		logger.Error("V2 BlobStore 初始化失败", "err", err)
		os.Exit(1)
	}

	datasetRepo := repository.NewDatasetRepo(db, cfg.FTS.TSConfig)
	taskRepo := repository.NewTaskRepo(db)
	archiveRepo := repository.NewArchiveRepo(db)
	metadataRepo := repository.NewMetadataRepo(db)
	pipelineRepo := repository.NewPipelineRepo(db)
	// 数据集动态索引管理器：FTS/筛选索引随数据集 schema 建删（表达式索引）
	datasetIndexer := repository.NewDatasetIndexer(db, cfg.FTS.TSConfig)

	/* ---- app 用例 ---- */
	parseSvc := app.NewParseService(docParser)
	analyzeSvc := app.NewAnalyzeService(llmClient, cfg.Workspace.DemandDir)
	if cfg.LLM.AgentMode {
		// agent 模式：工具按运行构造（read/search 文档工具 + write 草稿工具 + ask 人工交互），
		// 分析升级为「自主阅读 → 分批产出草稿 → 必要时问人」；草稿落库仍走人工确认的任务步骤
		analyzeSvc.EnableAgentMode(cfg.LLM.AgentMaxIterations)
		logger.Info("agent 模式已启用", "tools", "read_document/search_document/write_work_items/ask_human")
	}
	matchSvc := app.NewMatchService(datasetRepo, embedClient, cfg.Match.DuplicateThreshold)
	datasetWriter := app.NewDatasetWriter(embedClient, datasetRepo, cfg.Embedding.BatchSize)
	datasetQuery := app.NewDatasetQueryService(datasetRepo, embedClient)
	datasetAdmin := app.NewDatasetAdminService(datasetRepo, datasetIndexer, metadataRepo)
	archiveSvc := app.NewArchiveService(taskRepo, datasetRepo, archiveRepo, datasetIndexer)
	overviewSvc := app.NewOverviewService(datasetRepo, taskRepo)
	metadataSvc := app.NewMetadataService(metadataRepo, datasetRepo)
	// 元数据覆盖层装载（seed → override → effective）：先于任何用例构造之后、服务就绪之前
	if err := metadataSvc.Reload(context.Background()); err != nil {
		logger.Error("元数据覆盖层装载失败", "err", err)
		os.Exit(1)
	}
	taskMgr := app.NewTaskManager(taskRepo, parseSvc, analyzeSvc, datasetRepo, datasetWriter)
	// 服务重启恢复：把中断在 running 的任务/步骤标为 paused（用户手动继续）
	if err := taskMgr.Recover(context.Background()); err != nil {
		logger.Warn("任务恢复失败", "err", err)
	}

	v2Assets, err := apppipeline.NewAssetService(pipelineRepo, localBlobs, docParser,
		int64(cfg.Parser.MaxFileMB)<<20)
	if err != nil {
		logger.Error("V2 Asset Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	sourceParseExecutor, err := apppipeline.NewSourceParseExecutor(v2Assets)
	if err != nil {
		logger.Error("source.parse Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Extractions, err := apppipeline.NewExtractionService(pipelineRepo, llmClient,
		cfg.LLM.Model, apppipeline.ExtractionOptions{})
	if err != nil {
		logger.Error("V2 Extraction Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	llmExtractExecutor, err := apppipeline.NewLLMExtractExecutor(v2Extractions)
	if err != nil {
		logger.Error("llm.extract Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Cleaning, err := apppipeline.NewCleaningService(pipelineRepo)
	if err != nil {
		logger.Error("V2 Cleaning Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	dataTransformExecutor, err := apppipeline.NewDataTransformExecutor(v2Cleaning)
	if err != nil {
		logger.Error("data.transform Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	dataValidateExecutor, err := apppipeline.NewDataValidateExecutor(v2Cleaning)
	if err != nil {
		logger.Error("data.validate Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	// V2 Registry 只注册已经具备真实资源持久化与恢复语义的 Executor。
	v2Registry, err := apporchestrator.NewRegistry(sourceParseExecutor, llmExtractExecutor,
		dataTransformExecutor, dataValidateExecutor)
	if err != nil {
		logger.Error("V2 Executor Registry 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Definitions := apporchestrator.NewDefinitionService(pipelineRepo, v2Registry, pipelineRepo)
	v2Datasets := apppipeline.NewDatasetService(pipelineRepo)
	v2Scheduler := apporchestrator.NewScheduler(pipelineRepo)
	v2Worker, err := apporchestrator.NewWorker(pipelineRepo, v2Registry, v2Scheduler, apporchestrator.WorkerOptions{})
	if err != nil {
		logger.Error("V2 Worker 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Runtime, err := apporchestrator.NewRuntimeService(pipelineRepo, v2Scheduler, v2Worker)
	if err != nil {
		logger.Error("V2 Runtime 初始化失败", "err", err)
		os.Exit(1)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := v2Worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("V2 Worker 异常退出", "err", err)
		}
	}()

	var settingsView app.SettingsView
	settingsView.WorkspaceName = cfg.Workspace.Name
	settingsView.LLM.BaseURL, settingsView.LLM.Model, settingsView.LLM.Configured = cfg.LLM.BaseURL, cfg.LLM.Model, cfg.LLMReady()
	settingsView.LLM.AgentMode = cfg.LLM.AgentMode
	settingsView.Embedding.BaseURL, settingsView.Embedding.Model, settingsView.Embedding.Configured = cfg.Embedding.BaseURL, cfg.Embedding.Model, cfg.EmbeddingReady()
	settingsView.MinerU.Enabled, settingsView.MinerU.Configured = cfg.Parser.MinerU.Enabled, cfg.MinerUReady()
	settingsSvc := app.NewSettingsService(settingsView, llmClient)

	/* ---- HTTP ---- */
	engine := httpgin.New(httpgin.Services{
		Tasks: taskMgr, Match: matchSvc, Settings: settingsSvc, Overview: overviewSvc,
		DatasetQuery: datasetQuery, DatasetAdmin: datasetAdmin, Archive: archiveSvc, Metadata: metadataSvc,
		V2Definitions: v2Definitions, V2Runtime: v2Runtime, V2Datasets: v2Datasets,
		V2Assets: v2Assets, V2Extractions: v2Extractions, V2Cleaning: v2Cleaning,
		UploadDir: cfg.Workspace.UploadDir, MaxFileMB: int64(cfg.Parser.MaxFileMB),
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
	stopWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	select {
	case <-workerDone:
	case <-ctx.Done():
		logger.Warn("V2 Worker 未在关闭窗口内退出")
	}
}

// checkExampleTemplate 检查入库的示例模板是否被填入真实密钥（防止随代码分享泄漏）。
func checkExampleTemplate() []string {
	return config.CheckExampleLeak("config.example.yaml")
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
