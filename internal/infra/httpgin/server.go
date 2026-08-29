package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
)

// Services 组装点注入的全部业务用例（cmd 负责构造）。
type Services struct {
	Tasks         *app.TaskManager
	Match         *app.MatchService
	Settings      *app.SettingsService
	Overview      *app.OverviewService
	DatasetQuery  *app.DatasetQueryService
	DatasetAdmin  *app.DatasetAdminService
	Archive       *app.ArchiveService
	Metadata      *app.MetadataService
	V2Definitions *apporchestrator.DefinitionService
	V2Runtime     *apporchestrator.RuntimeService
	V2Datasets    *apppipeline.DatasetService
	V2Assets      *apppipeline.AssetService

	UploadDir string // 上传暂存目录（cmd 从配置注入）
	MaxFileMB int64  // 上传大小上限
}

// New 构造 HTTP 路由。
func New(svc Services) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	h := &handlers{svc: svc}
	api := r.Group("/api")
	{
		api.GET("/health", h.health)
		api.GET("/overview", h.overview)

		api.POST("/tasks", h.createTask)
		api.GET("/tasks", h.listTasks)
		api.GET("/tasks/:id", h.getTask)
		api.PATCH("/tasks/:id", h.patchTask)
		api.DELETE("/tasks/:id", h.archiveTask)   // 归档（可恢复，退出主业务循环）
		api.POST("/tasks/:id/items", h.taskItems) // 门内草稿批量保存
		api.POST("/tasks/:id/parse", h.taskParse) // fire-and-forget 步骤触发
		api.POST("/tasks/:id/analyze", h.taskAnalyze)
		api.POST("/tasks/:id/dataset", h.taskGenerateDataset)
		api.POST("/tasks/:id/dataset/preview", h.taskDatasetPreview) // 写入预览（冲突分桶）
		api.POST("/tasks/:id/pause", h.pauseTask)
		api.POST("/tasks/:id/resume", h.resumeTask)
		api.POST("/tasks/:id/complete", h.completeTask)
		api.POST("/tasks/:id/dialog", h.answerDialog) // 人工回答 agent 的提问（ask_human）
		api.POST("/tasks/:id/events", h.taskEvents)   // SSE：快照回放 + 实时

		api.GET("/workflows", h.listWorkflows)             // 任务类型目录（工作流元数据）
		api.GET("/datasets", h.listDatasets)               // 数据集浏览（任务产出的结果集）
		api.GET("/datasets/schemas", h.listDatasetSchemas) // 数据集类型模板目录（新建数据集带出）
		api.POST("/datasets", h.createDataset)             // 新建数据集（字段定义从模板带出或自定义）
		api.GET("/datasets/:id", h.getDataset)
		api.GET("/datasets/:id/items", h.queryDatasetItems)          // 条目筛选 + 语义检索
		api.POST("/datasets/:id/search", h.searchDatasetFTS)         // 字段全文检索（FTS 动态索引）
		api.POST("/datasets/:id/schema/check", h.datasetSchemaCheck) // 字段定义 dry-run
		api.PUT("/datasets/:id/schema", h.datasetSchemaUpdate)       // 字段定义受控保存
		api.DELETE("/datasets/:id", h.archiveDataset)                // 归档（含条目与向量，可恢复）

		api.GET("/archives", h.listArchives)                      // 归档列表
		api.POST("/archives/:kind/:id/restore", h.restoreArchive) // 归档恢复

		api.GET("/metadata", h.metadataCatalog)                       // 元数据目录总览
		api.GET("/metadata/task-types/:type", h.metadataTaskType)     // 任务类型聚合视图
		api.POST("/metadata/render/preview", h.metadataPromptPreview) // 提示词预览

		// 元数据受控编辑（M3）：写路径是显式管理动作，每次写必记审计
		api.POST("/metadata/schemas/:type/check", h.metadataSchemaCheck)      // 兼容性 dry-run
		api.PUT("/metadata/schemas/:type", h.metadataSchemaUpdate)            // schema 受控保存
		api.DELETE("/metadata/schemas/:type", h.metadataSchemaReset)          // 回退到内置
		api.PUT("/metadata/profiles/:type", h.metadataProfileUpdate)          // 指令头/示例编辑
		api.DELETE("/metadata/profiles/:type", h.metadataProfileReset)        // 回退到内置
		api.POST("/metadata/workflows/:type/check", h.metadataWorkflowCheck)  // 工作流 dry-run（M4）
		api.PUT("/metadata/workflows/:type", h.metadataWorkflowUpdate)        // 工作流受控保存（M4）
		api.DELETE("/metadata/workflows/:type", h.metadataWorkflowReset)      // 回退到内置工作流（M4）
		api.PUT("/metadata/workflows/:type/status", h.metadataWorkflowStatus) // 启用/停用向导类型（M4）
		api.POST("/metadata/task-types", h.metadataTaskTypeRegister)          // 新任务类型向导注册（M4）
		api.GET("/metadata/history/:kind/:key", h.metadataHistory)            // 版本历史
		api.GET("/metadata/export", h.metadataExport)                         // effective 视图导出
		api.POST("/metadata/import", h.metadataImport)                        // 导入（同一守卫）

		api.POST("/match/duplicates", h.checkDuplicates)

		api.GET("/settings", h.viewSettings)
		api.POST("/settings/test-llm", h.testLLM)
	}
	v2 := api.Group("/v2")
	if svc.V2Datasets != nil {
		v2.POST("/schemas", h.v2CreateSchema)
		v2.POST("/datasets", h.v2CreateDataset)
		v2.POST("/datasets/:id/batches", h.v2CreateBatch)
		v2.POST("/batches/:id/commit", h.v2CommitBatch)
		v2.GET("/datasets/:id/items", h.v2ListDatasetItems)
	}
	if svc.V2Definitions != nil && svc.V2Runtime != nil {
		v2.POST("/task-definitions", h.v2CreateTaskDefinition)
		v2.POST("/tasks", h.v2CreateTask)
		v2.GET("/tasks/:id", h.v2GetTask)
		v2.POST("/tasks/:id/start", h.v2StartTask)
		v2.POST("/tasks/:id/pause", h.v2PauseTask)
		v2.POST("/tasks/:id/resume", h.v2ResumeTask)
		v2.POST("/tasks/:id/steps/:step_id/retry", h.v2RetryStep)
		v2.POST("/tasks/:id/steps/:step_id/approve", h.v2ApproveStep)
		v2.GET("/tasks/:id/events", h.v2TaskEvents)
	}
	if svc.V2Assets != nil {
		v2.POST("/assets", h.v2UploadAsset)
		v2.POST("/asset-sets", h.v2CreateAssetSet)
		v2.GET("/asset-sets/:id", h.v2GetAssetSet)
		v2.GET("/parsed-document-sets/:id", h.v2GetParsedDocumentSet)
		v2.GET("/parsed-documents/:id/blocks", h.v2GetDocumentBlocks)
	}
	return r
}

type handlers struct {
	svc Services
}
