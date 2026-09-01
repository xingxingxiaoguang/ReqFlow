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
	appextraction "reqflow/internal/app/extraction"
	"reqflow/internal/app/pipeline"
	appplatformconfig "reqflow/internal/app/platformconfig"
	appretrieval "reqflow/internal/app/retrieval"
	appworkflow "reqflow/internal/app/workflow"
	domain "reqflow/internal/domain/workflow"
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
		if writeErr := os.WriteFile("config.yaml", []byte(config.Example()), 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "未找到配置文件且生成模板失败: %v\n", writeErr)
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
		for _, item := range errs {
			logger.Error("配置错误", "detail", item)
		}
		for _, item := range warns {
			logger.Warn("配置提醒", "detail", item)
		}
		os.Exit(1)
	} else {
		for _, item := range warns {
			logger.Warn("配置提醒", "detail", item)
		}
	}
	if filled := cfg.FilledSecrets(); len(filled) > 0 {
		logger.Info("已加载密钥（仅显示名称）", "keys", strings.Join(filled, ", "))
	}
	if leaked := config.CheckExampleLeak("config.example.yaml"); len(leaked) > 0 {
		logger.Error("检测到示例配置中存在真实密钥", "fields", strings.Join(leaked, ", "))
	}

	db, err := database.Connect(cfg.Database.DSN, cfg.Database.RetryCount, cfg.Database.RetryIntervalMs)
	if err != nil {
		logger.Error("数据库连接失败", "err", err)
		os.Exit(1)
	}
	if cfg.Database.AutoMigrate {
		if err := database.Migrate(db); err != nil {
			logger.Error("数据库迁移失败", "err", err)
			os.Exit(1)
		}
	}

	platformConfigRepo := repository.NewPlatformConfigRepo(db)
	secretCipher, err := secretcrypto.NewOrCreate(cfg.Security.EncryptionKey, cfg.Security.EncryptionKeyFile)
	if err != nil {
		logger.Error("平台配置密钥初始化失败", "err", err)
		os.Exit(1)
	}
	platformConfigs, err := appplatformconfig.NewService(platformConfigRepo, secretCipher, appplatformconfig.Fallbacks{
		WorkspaceName: cfg.Workspace.Name,
		LLM: port.LLMRuntimeConfig{Provider: cfg.LLM.Provider, BaseURL: cfg.LLM.BaseURL, APIKey: cfg.LLM.APIKey,
			Model: cfg.LLM.Model, Temperature: cfg.LLM.Temperature, MaxTokens: cfg.LLM.MaxTokens, TimeoutMs: cfg.LLM.TimeoutMs},
		Embedding: port.EmbeddingRuntimeConfig{BaseURL: cfg.Embedding.BaseURL, APIKey: cfg.Embedding.APIKey,
			Model: cfg.Embedding.Model, Dimensions: cfg.Embedding.Dimensions, BatchSize: cfg.Embedding.BatchSize,
			TimeoutMs: cfg.Embedding.TimeoutMs},
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
	lexicalClient := opensearch.New(opensearch.Options{BaseURL: cfg.OpenSearch.BaseURL, Username: cfg.OpenSearch.Username,
		Password: cfg.OpenSearch.Password, IndexPrefix: cfg.OpenSearch.IndexPrefix,
		Timeout: time.Duration(cfg.OpenSearch.TimeoutMs) * time.Millisecond})
	docParser := parser.NewDynamic(cfg.Parser.MaxFileMB, platformConfigs)
	blobs, err := blobstore.NewLocal(cfg.Workspace.BlobDir)
	if err != nil {
		logger.Error("BlobStore 初始化失败", "err", err)
		os.Exit(1)
	}

	pipelineRepo := repository.NewPipelineRepo(db)
	workflowRepo := repository.NewWorkflowRepo(db)
	assets, err := pipeline.NewAssetService(pipelineRepo, blobs, docParser, int64(cfg.Parser.MaxFileMB)<<20)
	if err != nil {
		logger.Error("Asset 服务初始化失败", "err", err)
		os.Exit(1)
	}
	extractions, err := appextraction.NewService(pipelineRepo, llmClient, cfg.LLM.Model, appextraction.Options{
		MaxIterations: cfg.LLM.AgentMaxIterations, ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("Extraction 服务初始化失败", "err", err)
		os.Exit(1)
	}
	cleaning, err := pipeline.NewCleaningService(pipelineRepo)
	if err != nil {
		logger.Error("Cleaning 服务初始化失败", "err", err)
		os.Exit(1)
	}
	datasets := pipeline.NewDatasetService(pipelineRepo)
	queryDatasets, err := pipeline.NewQueryDatasetService(pipelineRepo, datasets)
	if err != nil {
		logger.Error("Query Dataset 服务初始化失败", "err", err)
		os.Exit(1)
	}
	retrieval, err := appretrieval.NewService(pipelineRepo, lexicalClient, embedClient, rerankClient,
		appretrieval.Options{EmbeddingModel: cfg.Embedding.Model, PageSize: cfg.Embedding.BatchSize,
			ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("Retrieval 服务初始化失败", "err", err)
		os.Exit(1)
	}
	analysis, err := appanalysis.NewService(pipelineRepo, retrieval, llmClient,
		appanalysis.Options{Model: cfg.LLM.Model, MaxIterations: cfg.LLM.AgentMaxIterations,
			ConfigResolver: platformConfigs})
	if err != nil {
		logger.Error("Analysis 服务初始化失败", "err", err)
		os.Exit(1)
	}
	artifacts, err := appanalysis.NewArtifactService(pipelineRepo, blobs)
	if err != nil {
		logger.Error("Artifact 服务初始化失败", "err", err)
		os.Exit(1)
	}
	publish, err := pipeline.NewPublishService(pipelineRepo, datasets)
	if err != nil {
		logger.Error("Publish 服务初始化失败", "err", err)
		os.Exit(1)
	}

	sourceParse, err := pipeline.NewWorkflowSourceParseExecutor(assets)
	if err != nil {
		logger.Error("source.parse 初始化失败", "err", err)
		os.Exit(1)
	}
	transform, err := pipeline.NewWorkflowDataTransformExecutor(cleaning)
	if err != nil {
		logger.Error("data.transform 初始化失败", "err", err)
		os.Exit(1)
	}
	validate, err := pipeline.NewWorkflowDataValidateExecutor(cleaning)
	if err != nil {
		logger.Error("data.validate 初始化失败", "err", err)
		os.Exit(1)
	}
	dataPublish, err := pipeline.NewWorkflowDataPublishExecutor(publish)
	if err != nil {
		logger.Error("data.publish 初始化失败", "err", err)
		os.Exit(1)
	}
	humanReview, err := appworkflow.NewManualExecutor(domain.CapabilityRef{Kind: "human.review_records", Version: 1}, "人工审核记录")
	if err != nil {
		logger.Error("人工审核 Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	humanAnalysis, err := appworkflow.NewManualExecutor(domain.CapabilityRef{Kind: "human.approve_analysis", Version: 1}, "人工确认分析")
	if err != nil {
		logger.Error("人工分析 Executor 初始化失败", "err", err)
		os.Exit(1)
	}
	registry, err := appworkflow.NewNodeExecutorRegistry(sourceParse, transform, validate, dataPublish, humanReview, humanAnalysis)
	if err != nil {
		logger.Error("Workflow Executor Registry 初始化失败", "err", err)
		os.Exit(1)
	}
	runtime, err := appworkflow.NewRuntimeService(workflowRepo, workflowRepo, registry,
		time.Duration(cfg.Worker.LeaseSeconds)*time.Second, 2)
	if err != nil {
		logger.Error("Workflow Runtime 初始化失败", "err", err)
		os.Exit(1)
	}
	worker, err := appworkflow.NewRuntimeWorker(runtime, "", time.Duration(cfg.Worker.PollIntervalMs)*time.Millisecond)
	if err != nil {
		logger.Error("Workflow Worker 初始化失败", "err", err)
		os.Exit(1)
	}
	catalog, err := appworkflow.BuiltinCatalog()
	if err != nil {
		logger.Error("Capability Catalog 初始化失败", "err", err)
		os.Exit(1)
	}
	workflows, err := appworkflow.NewDraftService(workflowRepo, catalog)
	if err != nil {
		logger.Error("Workflow Draft 服务初始化失败", "err", err)
		os.Exit(1)
	}
	previews, err := appworkflow.NewPreviewService(workflowRepo, catalog)
	if err != nil {
		logger.Error("Workflow Preview 服务初始化失败", "err", err)
		os.Exit(1)
	}
	publications, err := appworkflow.NewPublicationService(workflowRepo, catalog)
	if err != nil {
		logger.Error("Workflow Publication 服务初始化失败", "err", err)
		os.Exit(1)
	}
	design, err := appworkflow.NewDesignService(workflowRepo, workflows, llmClient, catalog)
	if err != nil {
		logger.Error("Workflow Design 服务初始化失败", "err", err)
		os.Exit(1)
	}

	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := worker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("Workflow Worker 异常退出", "err", err)
		}
	}()

	engine := httpgin.New(httpgin.Services{PlatformConfigs: platformConfigs, V2Assets: assets, V2Extractions: extractions,
		V2Cleaning: cleaning, V2Datasets: datasets, V2QueryDatasets: queryDatasets, V2Retrieval: retrieval, V2Analysis: analysis,
		V2Artifacts: artifacts, Workflows: workflows, WorkflowPreviews: previews, WorkflowPublications: publications,
		WorkflowDesign: design, WorkflowRuntime: runtime, MaxFileMB: int64(cfg.Parser.MaxFileMB)})
	mountStatic(engine)
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Server.Port), Handler: engine}
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
	stopWorker()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	select {
	case <-workerDone:
	case <-ctx.Done():
		logger.Warn("Workflow Worker 未在关闭窗口内退出")
	}
}

func parseLevel(value string) slog.Level {
	switch value {
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
