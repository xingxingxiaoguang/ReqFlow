package httpgin

import (
	"github.com/gin-gonic/gin"

	appanalysis "reqflow/internal/app/analysis"
	appextraction "reqflow/internal/app/extraction"
	"reqflow/internal/app/pipeline"
	appplatformconfig "reqflow/internal/app/platformconfig"
	appretrieval "reqflow/internal/app/retrieval"
	appworkflow "reqflow/internal/app/workflow"
)

type Services struct {
	PlatformConfigs      *appplatformconfig.Service
	V2Datasets           *pipeline.DatasetService
	V2QueryDatasets      *pipeline.QueryDatasetService
	V2Assets             *pipeline.AssetService
	V2Extractions        *appextraction.Service
	V2Cleaning           *pipeline.CleaningService
	V2Retrieval          *appretrieval.Service
	V2Analysis           *appanalysis.Service
	V2Artifacts          *appanalysis.ArtifactService
	Workflows            *appworkflow.DraftService
	WorkflowPreviews     *appworkflow.PreviewService
	WorkflowPublications *appworkflow.PublicationService
	WorkflowDesign       *appworkflow.DesignService
	WorkflowRuntime      *appworkflow.RuntimeService
	MaxFileMB            int64
}

func New(svc Services) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())
	h := &handlers{svc: svc}
	api := r.Group("/api")
	api.GET("/health", h.health)
	if svc.PlatformConfigs != nil {
		api.GET("/platform-configs", h.listPlatformConfigs)
		api.POST("/platform-configs/:kind", h.createPlatformConfig)
		api.PUT("/platform-configs/:kind/:id", h.updatePlatformConfig)
		api.DELETE("/platform-configs/:kind/:id", h.deletePlatformConfig)
		api.POST("/platform-configs/:kind/:id/activate", h.activatePlatformConfig)
	}
	if svc.Workflows != nil {
		api.GET("/capabilities", h.workflowCapabilities)
		api.POST("/workflows", h.createWorkflow)
		api.GET("/workflows", h.listWorkflows)
		api.GET("/workflows/:id", h.getWorkflow)
		api.POST("/workflows/:id/commands", h.executeWorkflowCommand)
		api.POST("/workflows/:id/validate", h.validateWorkflow)
	}
	if svc.WorkflowPreviews != nil {
		api.POST("/workflows/:id/previews", h.createWorkflowPreview)
		api.GET("/workflow-previews/:id", h.getWorkflowPreview)
		api.POST("/workflows/:id/acceptance-cases/:case_id/run", h.runWorkflowAcceptance)
	}
	if svc.WorkflowPublications != nil {
		api.POST("/workflows/:id/publish", h.publishWorkflow)
		api.GET("/workflows/:id/revisions", h.listWorkflowRevisions)
		api.GET("/workflow-revisions/:id", h.getWorkflowRevision)
	}
	if svc.WorkflowDesign != nil {
		api.POST("/workflows/:id/design-sessions", h.createDesignSession)
		api.GET("/design-sessions/:id", h.getDesignSession)
		api.POST("/design-sessions/:id/messages", h.runDesignSession)
		api.POST("/design-sessions/:id/questions/:question_id/answer", h.answerDesignQuestion)
		api.POST("/design-sessions/:id/proposals/:proposal_id/accept", h.acceptDesignProposal)
		api.POST("/design-sessions/:id/proposals/:proposal_id/reject", h.rejectDesignProposal)
		api.POST("/design-sessions/:id/manual", h.switchDesignManual)
	}
	if svc.WorkflowRuntime != nil {
		api.POST("/workflow-runs", h.createWorkflowRun)
		api.GET("/workflow-runs", h.listWorkflowRuns)
		api.GET("/workflow-runs/:id", h.getWorkflowRun)
		api.POST("/workflow-runs/:id/start", h.startWorkflowRun)
		api.POST("/workflow-runs/:id/pause", h.pauseWorkflowRun)
		api.POST("/workflow-runs/:id/resume", h.resumeWorkflowRun)
		api.POST("/workflow-runs/:id/nodes/:node_id/retry", h.retryWorkflowNode)
		api.POST("/workflow-runs/:id/nodes/:node_id/manual-completion", h.completeWorkflowNodeManual)
	}
	if svc.V2Datasets != nil {
		api.POST("/schemas", h.v2CreateSchema)
		api.GET("/schemas/:id", h.v2GetSchema)
		api.POST("/datasets", h.v2CreateDataset)
		api.GET("/datasets/:id", h.v2GetDataset)
		api.POST("/datasets/:id/batches", h.v2CreateBatch)
		api.POST("/batches/:id/commit", h.v2CommitBatch)
		api.GET("/datasets/:id/items", h.v2ListDatasetItems)
	}
	if svc.V2QueryDatasets != nil {
		api.GET("/pipeline-cursors", h.v2GetPipelineCursor)
	}
	if svc.V2Assets != nil {
		api.POST("/assets", h.v2UploadAsset)
		api.POST("/asset-sets", h.v2CreateAssetSet)
		api.GET("/asset-sets/:id", h.v2GetAssetSet)
		api.GET("/parsed-document-sets/:id", h.v2GetParsedDocumentSet)
		api.GET("/parsed-documents/:id/blocks", h.v2GetDocumentBlocks)
	}
	if svc.V2Extractions != nil {
		api.POST("/extraction-profiles", h.v2CreateExtractionProfile)
		api.GET("/extraction-profiles/:id", h.v2GetExtractionProfile)
		api.GET("/record-draft-sets/:id", h.v2GetRecordDraftSet)
	}
	if svc.V2Cleaning != nil {
		api.GET("/transformed-record-sets/:id", h.v2GetTransformedRecordSet)
		api.GET("/validation-result-sets/:id", h.v2GetValidationResultSet)
	}
	if svc.V2Retrieval != nil {
		api.POST("/retrieval-profiles", h.v2CreateRetrievalProfile)
		api.GET("/retrieval-profiles/:id", h.v2GetRetrievalProfile)
		api.POST("/retrieval-profiles/:id/clone", h.v2CloneRetrievalProfile)
		api.GET("/retrieval-snapshots", h.v2ListRetrievalSnapshots)
		api.GET("/retrieval-snapshots/:id", h.v2GetRetrievalSnapshot)
		api.POST("/retrieval/search", h.v2SearchRetrieval)
	}
	if svc.V2Analysis != nil {
		api.POST("/analysis-profiles", h.v2CreateAnalysisProfile)
		api.GET("/analysis-profiles/:id", h.v2GetAnalysisProfile)
		api.POST("/analysis-profiles/:id/clone", h.v2CloneAnalysisProfile)
		api.GET("/analysis-results/:id", h.v2GetAnalysisResult)
	}
	if svc.V2Artifacts != nil {
		api.GET("/artifacts", h.v2ListArtifacts)
		api.GET("/artifacts/:id", h.v2GetArtifact)
		api.GET("/artifacts/:id/content", h.v2DownloadArtifact)
	}
	return r
}

type handlers struct{ svc Services }
