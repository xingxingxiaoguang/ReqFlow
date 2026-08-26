package httpgin

import (
	"github.com/gin-gonic/gin"

	"reqflow/internal/app"
)

// Services 组装点注入的全部业务用例（cmd 负责构造）。
type Services struct {
	Tasks    *app.TaskManager
	Match    *app.MatchService
	Settings *app.SettingsService
	Overview *app.OverviewService

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
		api.POST("/tasks/:id/items", h.taskItems)            // 门内草稿批量保存
		api.POST("/tasks/:id/parse", h.taskParse)            // fire-and-forget 步骤触发
		api.POST("/tasks/:id/analyze", h.taskAnalyze)
		api.POST("/tasks/:id/dataset", h.taskGenerateDataset)
		api.POST("/tasks/:id/pause", h.pauseTask)
		api.POST("/tasks/:id/resume", h.resumeTask)
		api.POST("/tasks/:id/complete", h.completeTask)
		api.POST("/tasks/:id/events", h.taskEvents) // SSE：快照回放 + 实时

		api.GET("/workflows", h.listWorkflows) // 任务类型目录（工作流元数据）
		api.GET("/datasets", h.listDatasets)   // 数据集浏览（任务产出的结果集）
		api.GET("/datasets/:id", h.getDataset)

		api.POST("/match/duplicates", h.checkDuplicates)

		api.GET("/settings", h.viewSettings)
		api.POST("/settings/test-llm", h.testLLM)
	}
	return r
}

type handlers struct {
	svc Services
}
