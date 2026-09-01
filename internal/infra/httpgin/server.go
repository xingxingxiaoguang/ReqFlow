package httpgin

import (
	"github.com/gin-gonic/gin"

	appanalysis "reqflow/internal/app/analysis"
	appcatalog "reqflow/internal/app/catalog"
	appextraction "reqflow/internal/app/extraction"
	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	appplatformagent "reqflow/internal/app/platformagent"
	appplatformconfig "reqflow/internal/app/platformconfig"
	appretrieval "reqflow/internal/app/retrieval"
)

// Services 组装点注入的全部业务用例（cmd 负责构造）。
type Services struct {
	PlatformConfigs *appplatformconfig.Service
	V2Definitions   *apporchestrator.DefinitionService
	V2TaskBatches   *apporchestrator.TaskBatchService
	V2Runtime       *apporchestrator.RuntimeService
	V2TaskQueries   *apporchestrator.TaskQueryService
	V2Datasets      *apppipeline.DatasetService
	V2QueryDatasets *apppipeline.QueryDatasetService
	V2Assets        *apppipeline.AssetService
	V2Extractions   *appextraction.Service
	V2Cleaning      *apppipeline.CleaningService
	V2Review        *apppipeline.ReviewService
	V2Retrieval     *appretrieval.Service
	V2Analysis      *appanalysis.Service
	V2Artifacts     *appanalysis.ArtifactService
	V2Catalog       *appcatalog.Service
	V2Agent         *appplatformagent.Service

	MaxFileMB int64 // 上传大小上限
}

// New 构造 HTTP 路由：业务能力一律挂 /api/v2，/api 下只保留健康检查。
func New(svc Services) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	h := &handlers{svc: svc}
	api := r.Group("/api")
	api.GET("/health", h.health)

	v2 := api.Group("/v2")
	if svc.PlatformConfigs != nil {
		v2.GET("/platform-configs", h.listPlatformConfigs)
		v2.POST("/platform-configs/:kind", h.createPlatformConfig)
		v2.PUT("/platform-configs/:kind/:id", h.updatePlatformConfig)
		v2.DELETE("/platform-configs/:kind/:id", h.deletePlatformConfig)
		v2.POST("/platform-configs/:kind/:id/activate", h.activatePlatformConfig)
	}
	if svc.V2Agent != nil {
		v2.GET("/agent/sessions", h.v2ListAgentSessions)
		v2.POST("/agent/sessions", h.v2CreateAgentSession)
		v2.GET("/agent/sessions/:id", h.v2GetAgentSession)
		v2.POST("/agent/sessions/:id/messages", h.v2RunAgentMessage)
		v2.POST("/agent/sessions/:id/stop", h.v2StopAgentMessage)
		v2.GET("/agent/config", h.v2GetAgentConfig)
		v2.POST("/agent/skills", h.v2CreateAgentSkill)
		v2.PUT("/agent/skills/:id/status", h.v2SetAgentSkillEnabled)
		v2.PUT("/agent/tools/:name/status", h.v2SetAgentToolEnabled)
	}
	if svc.V2Datasets != nil {
		v2.POST("/schemas", h.v2CreateSchema)
		if svc.V2Catalog != nil {
			v2.GET("/schemas", h.v2ListSchemas)
		}
		v2.GET("/schemas/:id", h.v2GetSchema)
		v2.POST("/datasets", h.v2CreateDataset)
		if svc.V2Catalog != nil {
			v2.GET("/datasets", h.v2ListDatasets)
			v2.GET("/datasets/:id/batches", h.v2ListDatasetBatches)
			v2.POST("/datasets/:id/archive", h.v2ArchiveDataset)
			v2.POST("/datasets/:id/restore", h.v2RestoreDataset)
		}
		v2.GET("/datasets/:id", h.v2GetDataset)
		v2.POST("/datasets/:id/batches", h.v2CreateBatch)
		v2.POST("/batches/:id/commit", h.v2CommitBatch)
		v2.GET("/datasets/:id/items", h.v2ListDatasetItems)
	}
	if svc.V2QueryDatasets != nil {
		v2.GET("/pipeline-cursors", h.v2GetPipelineCursor)
	}
	if svc.V2Definitions != nil && svc.V2Runtime != nil {
		v2.POST("/task-definitions", h.v2CreateTaskDefinition)
		v2.GET("/task-definitions/:id", h.v2GetTaskDefinition)
		v2.POST("/task-definitions/:id/archive", h.v2ArchiveTaskDefinition)
		v2.POST("/task-definitions/:id/restore", h.v2RestoreTaskDefinition)
		if svc.V2Catalog != nil {
			v2.GET("/task-definitions", h.v2ListTaskDefinitions)
		}
		v2.POST("/tasks", h.v2CreateTask)
		if svc.V2TaskBatches != nil {
			v2.POST("/task-batches", h.v2CreateTaskBatch)
		}
		v2.GET("/tasks/:id", h.v2GetTask)
		v2.POST("/tasks/:id/start", h.v2StartTask)
		v2.POST("/tasks/:id/pause", h.v2PauseTask)
		v2.POST("/tasks/:id/resume", h.v2ResumeTask)
		v2.POST("/tasks/:id/steps/:step_id/retry", h.v2RetryStep)
		v2.POST("/tasks/:id/steps/:step_id/approve-resource", h.v2ApproveResourceStep)
		if svc.V2Catalog != nil {
			v2.POST("/tasks/:id/archive", h.v2ArchiveTask)
			v2.POST("/tasks/:id/restore", h.v2RestoreTask)
		}
		if svc.V2Review != nil {
			v2.POST("/tasks/:id/steps/:step_id/approve", h.v2ApproveStep)
		}
		v2.GET("/tasks/:id/events", h.v2TaskEvents)
	}
	if svc.V2TaskQueries != nil {
		v2.GET("/tasks", h.v2ListTasks)
	}
	if svc.V2Assets != nil {
		v2.POST("/assets", h.v2UploadAsset)
		v2.POST("/asset-sets", h.v2CreateAssetSet)
		v2.GET("/asset-sets/:id", h.v2GetAssetSet)
		if svc.V2Catalog != nil {
			v2.GET("/asset-sets", h.v2ListAssetSets)
		}
		v2.GET("/parsed-document-sets/:id", h.v2GetParsedDocumentSet)
		v2.GET("/parsed-documents/:id/blocks", h.v2GetDocumentBlocks)
	}
	if svc.V2Extractions != nil {
		v2.POST("/extraction-profiles", h.v2CreateExtractionProfile)
		v2.GET("/extraction-profiles/:id", h.v2GetExtractionProfile)
		if svc.V2Catalog != nil {
			v2.GET("/extraction-profiles", h.v2ListExtractionProfiles)
		}
		v2.GET("/record-draft-sets/:id", h.v2GetRecordDraftSet)
	}
	if svc.V2Cleaning != nil {
		v2.GET("/transformed-record-sets/:id", h.v2GetTransformedRecordSet)
		v2.GET("/validation-result-sets/:id", h.v2GetValidationResultSet)
	}
	if svc.V2Review != nil {
		v2.GET("/approved-record-sets/:id", h.v2GetApprovedRecordSet)
	}
	if svc.V2Retrieval != nil {
		v2.POST("/retrieval-profiles", h.v2CreateRetrievalProfile)
		v2.GET("/retrieval-profiles", h.v2ListRetrievalProfiles)
		v2.GET("/retrieval-profiles/:id", h.v2GetRetrievalProfile)
		v2.POST("/retrieval-profiles/:id/clone", h.v2CloneRetrievalProfile)
		v2.GET("/retrieval-snapshots", h.v2ListRetrievalSnapshots)
		v2.GET("/retrieval-snapshots/:id", h.v2GetRetrievalSnapshot)
		v2.POST("/retrieval/search", h.v2SearchRetrieval)
	}
	if svc.V2Analysis != nil {
		v2.POST("/analysis-profiles", h.v2CreateAnalysisProfile)
		v2.GET("/analysis-profiles", h.v2ListAnalysisProfiles)
		v2.GET("/analysis-profiles/:id", h.v2GetAnalysisProfile)
		v2.POST("/analysis-profiles/:id/clone", h.v2CloneAnalysisProfile)
		v2.GET("/analysis-results/:id", h.v2GetAnalysisResult)
	}
	if svc.V2Artifacts != nil {
		v2.GET("/artifacts", h.v2ListArtifacts)
		v2.GET("/artifacts/:id", h.v2GetArtifact)
		v2.GET("/artifacts/:id/content", h.v2DownloadArtifact)
	}
	if svc.V2Catalog != nil {
		v2.GET("/archives", h.v2ListArchives)
	}
	return r
}

type handlers struct {
	svc Services
}
