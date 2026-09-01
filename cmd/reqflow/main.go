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

	appanalysis "reqflow/internal/app/analysis"
	appcatalog "reqflow/internal/app/catalog"
	appextraction "reqflow/internal/app/extraction"
	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	appplatformagent "reqflow/internal/app/platformagent"
	appplatformconfig "reqflow/internal/app/platformconfig"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/infra/blobstore"
	"reqflow/internal/infra/config"
	secretcrypto "reqflow/internal/infra/crypto"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/embedding"
	"reqflow/internal/infra/httpgin"
	"reqflow/internal/infra/llm"
	"reqflow/internal/infra/opensearch"
	"reqflow/internal/infra/parser"
	"reqflow/internal/infra/repository"
	"reqflow/internal/port"
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
	platformConfigRepo := repository.NewPlatformConfigRepo(db)
	secretCipher, err := secretcrypto.NewOrCreate(cfg.Security.EncryptionKey, cfg.Security.EncryptionKeyFile)
	if err != nil {
		logger.Error("平台配置密钥初始化失败", "err", err)
		os.Exit(1)
	}
	platformConfigs, err := appplatformconfig.NewService(platformConfigRepo, secretCipher, appplatformconfig.Fallbacks{
		WorkspaceName: cfg.Workspace.Name,
		LLM: port.LLMRuntimeConfig{Provider: cfg.LLM.Provider, BaseURL: cfg.LLM.BaseURL,
			APIKey: cfg.LLM.APIKey, Model: cfg.LLM.Model, Temperature: cfg.LLM.Temperature,
			MaxTokens: cfg.LLM.MaxTokens, TimeoutMs: cfg.LLM.TimeoutMs},
		Embedding: port.EmbeddingRuntimeConfig{BaseURL: cfg.Embedding.BaseURL, APIKey: cfg.Embedding.APIKey,
			Model: cfg.Embedding.Model, Dimensions: cfg.Embedding.Dimensions,
			BatchSize: cfg.Embedding.BatchSize, TimeoutMs: cfg.Embedding.TimeoutMs},
		Rerank: port.RerankRuntimeConfig{BaseURL: cfg.Embedding.BaseURL, APIKey: cfg.Embedding.APIKey,
			Model: cfg.Embedding.RerankModel, TimeoutMs: cfg.Embedding.RerankTimeoutMs},
		MinerU: port.MinerURuntimeConfig{Enabled: cfg.Parser.MinerU.Enabled, APIURL: cfg.Parser.MinerU.APIURL,
			APIToken: cfg.Parser.MinerU.APIToken, ModelVersion: cfg.Parser.MinerU.ModelVersion,
			TimeoutMs: cfg.Parser.MinerU.TimeoutMs, PollIntervalMs: cfg.Parser.MinerU.PollIntervalMs},
	})
	if err != nil {
		logger.Error("平台配置服务初始化失败", "err", err)
		os.Exit(1)
	}
	llmClient := llm.NewDynamic(platformConfigs)
	embedClient := embedding.NewDynamic(platformConfigs)
	rerankClient := embedding.NewDynamicReranker(platformConfigs)
	lexicalClient := opensearch.New(opensearch.Options{
		BaseURL: cfg.OpenSearch.BaseURL, Username: cfg.OpenSearch.Username,
		Password: cfg.OpenSearch.Password, IndexPrefix: cfg.OpenSearch.IndexPrefix,
		Timeout: time.Duration(cfg.OpenSearch.TimeoutMs) * time.Millisecond,
	})
	docParser := parser.NewDynamic(cfg.Parser.MaxFileMB, platformConfigs)
	localBlobs, err := blobstore.NewLocal(cfg.Workspace.BlobDir)
	if err != nil {
		logger.Error("V2 BlobStore 初始化失败", "err", err)
		os.Exit(1)
	}

	pipelineRepo := repository.NewPipelineRepo(db)
	agentSessionRepo := repository.NewAgentSessionRepo(db)
	agentConfigRepo := repository.NewAgentConfigRepo(db)

	/* ---- app 用例 ---- */
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
	v2Extractions, err := appextraction.NewService(pipelineRepo, llmClient,
		cfg.LLM.Model, appextraction.Options{MaxIterations: cfg.LLM.AgentMaxIterations,
			ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("V2 Extraction Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	documentExtractExecutor, err := appextraction.NewExecutor(v2Extractions)
	if err != nil {
		logger.Error("document.extract Executor 初始化失败", "err", err)
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
	v2Datasets := apppipeline.NewDatasetService(pipelineRepo)
	v2QueryDatasets, err := apppipeline.NewQueryDatasetService(pipelineRepo, v2Datasets)
	if err != nil {
		logger.Error("V2 Query Dataset 初始化失败", "err", err)
		os.Exit(1)
	}
	queryDatasetExecutor, err := apppipeline.NewQueryDatasetDeriveExecutor(v2QueryDatasets)
	if err != nil {
		logger.Error("data.query_derive Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Retrieval, err := appretrieval.NewService(pipelineRepo, lexicalClient, embedClient, rerankClient,
		appretrieval.Options{EmbeddingModel: cfg.Embedding.Model, PageSize: cfg.Embedding.BatchSize,
			ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("V2 Retrieval 初始化失败", "err", err)
		os.Exit(1)
	}
	retrievalBuildExecutor, err := appretrieval.NewBuildExecutor(v2Retrieval)
	if err != nil {
		logger.Error("retrieval.build Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Analysis, err := appanalysis.NewService(pipelineRepo, v2Retrieval, llmClient,
		appanalysis.Options{Model: cfg.LLM.Model, MaxIterations: cfg.LLM.AgentMaxIterations,
			ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("V2 Analysis 初始化失败", "err", err)
		os.Exit(1)
	}
	knowledgeAnalyzeExecutor, err := appanalysis.NewKnowledgeAnalyzeExecutor(v2Analysis)
	if err != nil {
		logger.Error("knowledge.analyze Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Artifacts, err := appanalysis.NewArtifactService(pipelineRepo, localBlobs)
	if err != nil {
		logger.Error("V2 Artifact 初始化失败", "err", err)
		os.Exit(1)
	}
	analysisPublishExecutor, err := appanalysis.NewAnalysisPublishExecutor(v2Analysis, v2Datasets)
	if err != nil {
		logger.Error("data.analysis_publish Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	artifactRenderExecutor, err := appanalysis.NewArtifactRenderExecutor(v2Analysis, v2Artifacts)
	if err != nil {
		logger.Error("artifact.render Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	graphBuildExecutor, err := appanalysis.NewGraphBuildExecutor(v2Analysis, v2Artifacts)
	if err != nil {
		logger.Error("graph.build Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Publish, err := apppipeline.NewPublishService(pipelineRepo, v2Datasets)
	if err != nil {
		logger.Error("V2 Publish Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	dataPublishExecutor, err := apppipeline.NewDataPublishExecutor(v2Publish)
	if err != nil {
		logger.Error("data.publish Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	// V2 Registry 只注册已经具备真实资源持久化与恢复语义的 Executor。
	v2Registry, err := apporchestrator.NewRegistry(sourceParseExecutor, documentExtractExecutor,
		dataTransformExecutor, dataValidateExecutor, dataPublishExecutor, queryDatasetExecutor,
		retrievalBuildExecutor, knowledgeAnalyzeExecutor, analysisPublishExecutor, artifactRenderExecutor,
		graphBuildExecutor)
	if err != nil {
		logger.Error("V2 Executor Registry 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Definitions := apporchestrator.NewDefinitionService(pipelineRepo, v2Registry, pipelineRepo)
	v2TaskBatches, err := apporchestrator.NewTaskBatchService(v2Definitions, v2Assets)
	if err != nil {
		logger.Error("V2 Task Batch 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Scheduler := apporchestrator.NewScheduler(pipelineRepo)
	v2Worker, err := apporchestrator.NewWorker(pipelineRepo, v2Registry, v2Scheduler, apporchestrator.WorkerOptions{
		Concurrency: cfg.Worker.Concurrency, LeaseDuration: time.Duration(cfg.Worker.LeaseSeconds) * time.Second,
		PollInterval:     time.Duration(cfg.Worker.PollIntervalMs) * time.Millisecond,
		RecoveryInterval: time.Duration(cfg.Worker.RecoveryIntervalMs) * time.Millisecond,
		ReconcileLimit:   cfg.Worker.ReconcileLimit,
	})
	if err != nil {
		logger.Error("V2 Worker 初始化失败", "err", err)
		os.Exit(1)
	}
	logger.Info("V2 Worker 协程池已初始化", "concurrency", cfg.Worker.Concurrency,
		"lease_seconds", cfg.Worker.LeaseSeconds)
	v2Runtime, err := apporchestrator.NewRuntimeService(pipelineRepo, v2Scheduler, v2Worker)
	if err != nil {
		logger.Error("V2 Runtime 初始化失败", "err", err)
		os.Exit(1)
	}
	v2TaskQueries, err := apporchestrator.NewTaskQueryService(pipelineRepo)
	if err != nil {
		logger.Error("V2 Task Query 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Catalog, err := appcatalog.NewService(pipelineRepo)
	if err != nil {
		logger.Error("V2 Catalog 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Review, err := apppipeline.NewReviewService(pipelineRepo, v2Runtime)
	if err != nil {
		logger.Error("V2 Review Pipeline 初始化失败", "err", err)
		os.Exit(1)
	}
	v2Agent, err := appplatformagent.NewService(agentSessionRepo, agentConfigRepo, llmClient, appplatformagent.Dependencies{
		Definitions: v2Definitions, Runtime: v2Runtime, Tasks: v2TaskQueries,
		Catalog: v2Catalog, Retrieval: v2Retrieval,
	}, appplatformagent.Options{MaxIterations: cfg.LLM.AgentMaxIterations})
	if err != nil {
		logger.Error("ReqFlow Agent 初始化失败", "err", err)
		os.Exit(1)
	}
	if err := v2Agent.Recover(context.Background()); err != nil {
		logger.Warn("ReqFlow Agent 会话恢复失败", "err", err)
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := v2Worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("V2 Worker 异常退出", "err", err)
		}
	}()

	/* ---- HTTP ---- */
	engine := httpgin.New(httpgin.Services{
		PlatformConfigs: platformConfigs,
		V2Definitions: v2Definitions, V2TaskBatches: v2TaskBatches,
		V2Runtime: v2Runtime, V2TaskQueries: v2TaskQueries,
		V2Datasets: v2Datasets, V2QueryDatasets: v2QueryDatasets,
		V2Assets: v2Assets, V2Extractions: v2Extractions, V2Cleaning: v2Cleaning, V2Review: v2Review,
		V2Retrieval: v2Retrieval, V2Analysis: v2Analysis, V2Artifacts: v2Artifacts,
		V2Catalog: v2Catalog, V2Agent: v2Agent,
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
