package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// Services 组装点注入的全部业务用例（cmd 负责构造）。
type Services struct {
	Sync     *app.SyncService
	Parse    *app.ParseService
	Analyze  *app.AnalyzeService
	Match    *app.MatchService
	Import   *app.ImportService
	Record   *app.RecordService
	Settings *app.SettingsService
	Overview *app.OverviewService
	Browse   *app.BrowseService

	UploadDir  string // 上传暂存目录（cmd 从配置注入）
	MaxFileMB  int64  // 上传大小上限
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

		api.POST("/parse", h.parseStream)         // SSE：解析确认门
		api.POST("/analyze", h.analyzeStream)     // SSE：LLM 流式分析
		api.POST("/sync", h.syncStream)           // SSE：增量同步
		api.POST("/import", h.importStream)       // SSE：批量导入
		api.POST("/match/projects", h.matchProjects)
		api.POST("/match/duplicates", h.checkDuplicates)

		api.GET("/records", h.listRecords)
		api.GET("/records/:id", h.getRecord)
		api.GET("/records/:id/source", h.recordSource)

		api.GET("/projects", h.listProjects)
		api.GET("/work-items", h.listWorkItems)

		api.GET("/settings", h.viewSettings)
		api.POST("/settings/test-llm", h.testLLM)
		api.POST("/settings/test-pingcode", h.testPingCode)
	}
	return r
}

type handlers struct {
	svc Services
}
